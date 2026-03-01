package engine

import (
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/reconify/reconify/config"
)

// ParseCSV reads a CSV file and returns normalized transactions according to the source config.
func ParseCSV(sourceName string, filePath string, cfg config.CSVParserCfg) ([]Transaction, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("open %q: %w", filePath, err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.TrimLeadingSpace = true

	// Read header row
	headers, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}
	colIndex := buildColIndex(headers)

	// Resolve timezone for date parsing
	loc := time.UTC
	if cfg.TZ != "" {
		if l, err := time.LoadLocation(cfg.TZ); err == nil {
			loc = l
		}
	}

	// Resolve decimal and thousands separators
	decimal := "."
	if cfg.Decimal != "" {
		decimal = cfg.Decimal
	}

	multiplier := cfg.Multiplier
	if multiplier <= 0 {
		multiplier = 1
	}

	var txns []Transaction
	rowNum := 0

	for {
		record, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read row %d: %w", rowNum+2, err)
		}
		rowNum++

		raw := make(map[string]string, len(headers))
		for i, h := range headers {
			if i < len(record) {
				raw[h] = record[i]
			}
		}

		// Date
		dateStr := getCol(record, colIndex, cfg.DateCol)
		if dateStr == "" {
			return nil, fmt.Errorf("row %d: date column %q is empty", rowNum+1, cfg.DateCol)
		}
		date, err := time.ParseInLocation(cfg.DateLayout, strings.TrimSpace(dateStr), loc)
		if err != nil {
			return nil, fmt.Errorf("row %d: parse date %q with layout %q: %w", rowNum+1, dateStr, cfg.DateLayout, err)
		}

		// Amount
		amtStr := getCol(record, colIndex, cfg.AmountCol)
		if amtStr == "" {
			return nil, fmt.Errorf("row %d: amount column %q is empty", rowNum+1, cfg.AmountCol)
		}
		amount, err := parseAmount(amtStr, decimal, cfg.Thousands, multiplier)
		if err != nil {
			return nil, fmt.Errorf("row %d: parse amount %q: %w", rowNum+1, amtStr, err)
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

		txns = append(txns, txn)
	}

	return txns, nil
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
