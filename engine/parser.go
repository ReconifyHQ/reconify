package engine

import (
	"bufio"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/reconifyhq/reconify/config"
)

// ParseCSVEach streams a CSV file, calling fn for each parsed transaction.
//
// Ownership: each call receives a distinct Transaction by value. The callback
// owns the value and may retain it without copying. No struct is reused between calls.
//
// Error format: all errors include filePath, row number (1-based), the original
// field value, and the source name. Example:
//
//	bank.csv: row 452133: source "bank": invalid date "2023-99-01" with layout "2006-01-02"
//
// Context: checked between rows. Returns ctx.Err() on cancellation.
func ParseCSVEach(
	ctx context.Context,
	sourceName string,
	filePath string,
	cfg config.CSVParserCfg,
	fn func(tx Transaction, rowNum int) error,
) error {
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open %q: %w", filePath, err)
	}
	defer func() {
		_ = f.Close()
	}()

	r := csv.NewReader(bufio.NewReaderSize(f, 1<<20))
	r.TrimLeadingSpace = true

	// Read header row
	headers, err := r.Read()
	if err != nil {
		return fmt.Errorf("%s: read header: %w", filePath, err)
	}
	colIndex := buildColIndex(headers)

	// Resolve timezone for date parsing
	loc := time.UTC
	if cfg.TZ != "" {
		if l, err := time.LoadLocation(cfg.TZ); err == nil {
			loc = l
		}
	}

	// Resolve decimal separator
	decimal := "."
	if cfg.Decimal != "" {
		decimal = cfg.Decimal
	}

	multiplier := cfg.Multiplier
	if multiplier <= 0 {
		multiplier = 1
	}

	// Date cache: capped at 1000 unique keys, then permanently disabled.
	// Eliminates repeated time.ParseInLocation calls for files with many same-date rows.
	// Disabled automatically when timestamp columns are detected (per-row unique values
	// fill the cache quickly and trigger permanent fallback to direct parsing).
	dateCache := make(map[string]time.Time)
	cacheEnabled := true

	rowNum := 0
	for {
		// Check for context cancellation between rows
		if ctx.Err() != nil {
			return ctx.Err()
		}

		record, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("%s: row %d: %w", filePath, rowNum+2, err)
		}
		rowNum++

		// Date
		dateStr := strings.TrimSpace(getCol(record, colIndex, cfg.DateCol))
		if dateStr == "" {
			return fmt.Errorf("%s: row %d: source %q: date column %q is empty",
				filePath, rowNum+1, sourceName, cfg.DateCol)
		}

		var date time.Time
		if cacheEnabled {
			if t, ok := dateCache[dateStr]; ok {
				date = t
			} else {
				t, parseErr := time.ParseInLocation(cfg.DateLayout, dateStr, loc)
				if parseErr != nil {
					return fmt.Errorf("%s: row %d: source %q: invalid date %q with layout %q: %w",
						filePath, rowNum+1, sourceName, dateStr, cfg.DateLayout, parseErr)
				}
				date = t
				if len(dateCache) >= 1000 {
					// Permanently disable cache and free memory.
					cacheEnabled = false
					dateCache = nil
				} else {
					dateCache[dateStr] = t
				}
			}
		} else {
			t, parseErr := time.ParseInLocation(cfg.DateLayout, dateStr, loc)
			if parseErr != nil {
				return fmt.Errorf("%s: row %d: source %q: invalid date %q with layout %q: %w",
					filePath, rowNum+1, sourceName, dateStr, cfg.DateLayout, parseErr)
			}
			date = t
		}

		// Amount
		amtStr := getCol(record, colIndex, cfg.AmountCol)
		if amtStr == "" {
			return fmt.Errorf("%s: row %d: source %q: amount column %q is empty",
				filePath, rowNum+1, sourceName, cfg.AmountCol)
		}
		amount, err := parseAmount(amtStr, decimal, cfg.Thousands, multiplier)
		if err != nil {
			return fmt.Errorf("%s: row %d: source %q: parse amount %q: %w",
				filePath, rowNum+1, sourceName, amtStr, err)
		}

		// Raw map — only allocated when SkipRaw is false
		var raw map[string]string
		if !cfg.SkipRaw {
			raw = make(map[string]string, len(headers))
			for i, h := range headers {
				if i < len(record) {
					raw[h] = record[i]
				}
			}
		}

		txn := Transaction{
			ID:        fmt.Sprintf("%s-%d", sourceName, rowNum),
			Date:      date,
			Amount:    amount,
			Source:    sourceName,
			Raw:       raw,
			Currency:  strings.TrimSpace(getCol(record, colIndex, cfg.CurrencyCol)),
			Reference: strings.TrimSpace(getCol(record, colIndex, cfg.RefCol)),
			Name:      strings.TrimSpace(getCol(record, colIndex, cfg.NameCol)),
		}

		if err := fn(txn, rowNum); err != nil {
			return err
		}
	}

	return nil
}

// ParseCSV reads a CSV file and returns normalized transactions according to the source config.
// It is a convenience wrapper around ParseCSVEach for callers that need a complete slice.
func ParseCSV(sourceName string, filePath string, cfg config.CSVParserCfg) ([]Transaction, error) {
	var txns []Transaction
	err := ParseCSVEach(context.Background(), sourceName, filePath, cfg, func(tx Transaction, _ int) error {
		txns = append(txns, tx)
		return nil
	})
	return txns, err
}

// buildColIndex builds a case-insensitive column name → index map.
func buildColIndex(headers []string) map[string]int {
	idx := make(map[string]int, len(headers))
	for i, h := range headers {
		idx[strings.ToLower(strings.TrimSpace(h))] = i
	}
	return idx
}

// getCol returns the value for a named column, case-insensitively. Returns "" if not found.
func getCol(record []string, colIndex map[string]int, colName string) string {
	if colName == "" {
		return ""
	}
	i, ok := colIndex[strings.ToLower(strings.TrimSpace(colName))]
	if !ok || i >= len(record) {
		return ""
	}
	return record[i]
}

// parseAmount parses an amount string to int64 minor units.
// It removes the thousands separator, normalizes the decimal separator to ".",
// parses as float64, multiplies by the multiplier, and rounds.
func parseAmount(s string, decimal string, thousands string, multiplier int64) (int64, error) {
	s = strings.TrimSpace(s)

	// Remove thousands separator
	if thousands != "" {
		s = strings.ReplaceAll(s, thousands, "")
	}

	// Normalize decimal separator
	if decimal != "." && decimal != "" {
		s = strings.ReplaceAll(s, decimal, ".")
	}

	// Handle parentheses for negative amounts: (1234.56) → -1234.56
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		s = "-" + s[1:len(s)-1]
	}

	// Remove any currency symbols or spaces that might remain
	s = strings.TrimSpace(s)

	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("not a number: %q", s)
	}

	return int64(math.Round(f * float64(multiplier))), nil
}
