// Package sample validates bounded raw input samples against a parser mapping.
package sample

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/reconifyhq/reconify/config"
	"github.com/reconifyhq/reconify/engine/parser"
)

// RowError identifies a row that could not be parsed using a source mapping.
type RowError struct {
	Row     int    `json:"row"`
	Message string `json:"message"`
}

// Result describes a bounded validation scan.
type Result struct {
	RowsScanned    int        `json:"rows_scanned"`
	SuccessfulRows int        `json:"successful_rows"`
	Truncated      bool       `json:"truncated"`
	Errors         []RowError `json:"errors,omitempty"`
}

// Validate checks up to limit raw rows against cfg. A non-positive limit scans
// every row. Invalid rows are collected so callers can report all sample errors.
func Validate(ctx context.Context, filePath string, cfg config.ParserCfg, limit int) (Result, error) {
	loc := time.UTC
	if cfg.TZ != "" {
		var err error
		loc, err = time.LoadLocation(cfg.TZ)
		if err != nil {
			return Result{}, fmt.Errorf("load timezone %q: %w", cfg.TZ, err)
		}
	}
	decimal := cfg.Decimal
	if decimal == "" {
		decimal = "."
	}
	result := Result{}
	_, truncated, err := parser.RawRowsEach(ctx, filePath, cfg, limit, func(row map[string]string, rowNum int) error {
		result.RowsScanned++
		date := strings.TrimSpace(value(row, cfg.DateCol))
		if date == "" {
			result.Errors = append(result.Errors, RowError{Row: rowNum, Message: fmt.Sprintf("date column %q is empty", cfg.DateCol)})
			return nil
		}
		if _, err := time.ParseInLocation(cfg.DateLayout, date, loc); err != nil {
			result.Errors = append(result.Errors, RowError{Row: rowNum, Message: fmt.Sprintf("invalid date %q with layout %q", date, cfg.DateLayout)})
			return nil
		}
		amount := strings.TrimSpace(value(row, cfg.AmountCol))
		if amount == "" {
			result.Errors = append(result.Errors, RowError{Row: rowNum, Message: fmt.Sprintf("amount column %q is empty", cfg.AmountCol)})
			return nil
		}
		if _, err := parser.ParseAmount(amount, decimal, cfg.Thousands, cfg.Multiplier); err != nil {
			result.Errors = append(result.Errors, RowError{Row: rowNum, Message: fmt.Sprintf("invalid amount %q", amount)})
			return nil
		}
		result.SuccessfulRows++
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	result.Truncated = truncated
	return result, nil
}

func value(row map[string]string, column string) string {
	want := strings.ToLower(strings.TrimSpace(column))
	for key, value := range row {
		if strings.ToLower(strings.TrimSpace(key)) == want {
			return value
		}
	}
	return ""
}
