//nolint:staticcheck // Domain aliases keep package-internal signatures readable.
package parser

import (
	"bufio"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"os"

	"github.com/reconifyhq/reconify/config"
	. "github.com/reconifyhq/reconify/engine/domain"
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
	csvCfg := cfg
	csvCfg.Type = "csv"
	return parseDelimitedEach(ctx, sourceName, filePath, csvCfg, fn)
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
func parseDelimitedEach(
	ctx context.Context,
	sourceName string,
	filePath string,
	cfg config.ParserCfg,
	fn func(tx Transaction, rowNum int) error,
) error {
	f, err := os.Open(filePath) // #nosec G304 -- parser input paths are explicit CLI/config/user-selected files.
	if err != nil {
		return fmt.Errorf("open %q: %w", filePath, err)
	}
	defer func() {
		_ = f.Close()
	}()

	r := csv.NewReader(bufio.NewReaderSize(f, 1<<20))
	r.TrimLeadingSpace = true

	headers, err := r.Read()
	if err != nil {
		return fmt.Errorf("%s: read header: %w", filePath, err)
	}

	normalizer := newRowNormalizer(sourceName, filePath, cfg)
	rowNum := 0
	for {
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

		tx, err := normalizer.fromRecord(headers, record, rowNum, rowNum+1)
		if err != nil {
			return err
		}
		if err := fn(tx, rowNum); err != nil {
			return err
		}
	}

	return nil
}
func readCSVHeaders(filePath string) ([]string, error) {
	f, err := os.Open(filePath) // #nosec G304 -- parser input paths are explicit CLI/config/user-selected files.
	if err != nil {
		return nil, fmt.Errorf("open %q: %w", filePath, err)
	}
	defer func() {
		_ = f.Close()
	}()
	r := csv.NewReader(bufio.NewReaderSize(f, 1<<20))
	r.TrimLeadingSpace = true
	headers, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("%s: read header: %w", filePath, err)
	}
	return headers, nil
}
