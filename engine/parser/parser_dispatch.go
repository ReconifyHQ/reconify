// Package parser normalizes configured source files into transactions.
//
//nolint:staticcheck // Domain aliases keep package-internal signatures readable.
package parser

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/reconifyhq/reconify/config"
	. "github.com/reconifyhq/reconify/engine/domain"
)

// ParseEach streams a supported input file, calling fn for each parsed transaction.
//
// Supported parser types are csv, json, xlsx, and auto. Empty or auto types are
// inferred from the file extension: .csv, .json, .ndjson, .xlsx, or .xlsm.
func ParseEach(
	ctx context.Context,
	sourceName string,
	filePath string,
	cfg config.ParserCfg,
	fn func(tx Transaction, rowNum int) error,
) error {
	parserType, err := ResolveParserType(filePath, cfg)
	if err != nil {
		return err
	}

	switch parserType {
	case "csv":
		return parseDelimitedEach(ctx, sourceName, filePath, cfg, fn)
	case "json":
		return parseJSONEach(ctx, sourceName, filePath, cfg, fn)
	case "xlsx":
		return parseXLSXEach(ctx, sourceName, filePath, cfg, fn)
	default:
		return fmt.Errorf("unsupported parser type %q", parserType)
	}
}

// Parse reads a supported input file and returns normalized transactions.
// It is a convenience wrapper around ParseEach for callers that need a complete slice.
func Parse(sourceName string, filePath string, cfg config.ParserCfg) ([]Transaction, error) {
	var txns []Transaction
	err := ParseEach(context.Background(), sourceName, filePath, cfg, func(tx Transaction, _ int) error {
		txns = append(txns, tx)
		return nil
	})
	return txns, err
}

// ResolveParserType returns the concrete parser type for a source config and file.
func ResolveParserType(filePath string, cfg config.ParserCfg) (string, error) {
	parserType := strings.ToLower(strings.TrimSpace(cfg.Type))
	if parserType != "" && parserType != "auto" {
		switch parserType {
		case "csv", "json", "xlsx":
			return parserType, nil
		default:
			return "", fmt.Errorf("unsupported parser type %q (valid: csv, json, xlsx, auto)", cfg.Type)
		}
	}

	switch strings.ToLower(filepath.Ext(filePath)) {
	case ".csv":
		return "csv", nil
	case ".json", ".ndjson":
		return "json", nil
	case ".xlsx", ".xlsm":
		return "xlsx", nil
	case ".xls":
		return "", fmt.Errorf("unsupported Excel format %q: legacy .xls files are not supported; save as .xlsx or .csv", filePath)
	default:
		return "", fmt.Errorf("cannot infer parser type for %q: supported extensions are .csv, .json, .ndjson, .xlsx, and .xlsm", filePath)
	}
}

// ReadInputHeaders returns the input field names visible to a parser.
// For JSON it returns the keys on the first object.
func ReadInputHeaders(ctx context.Context, filePath string, cfg config.ParserCfg) ([]string, error) {
	parserType, err := ResolveParserType(filePath, cfg)
	if err != nil {
		return nil, err
	}
	switch parserType {
	case "csv":
		return readCSVHeaders(filePath)
	case "json":
		return readJSONHeaders(ctx, filePath)
	case "xlsx":
		return readXLSXHeaders(ctx, filePath, cfg)
	default:
		return nil, fmt.Errorf("unsupported parser type %q", parserType)
	}
}
