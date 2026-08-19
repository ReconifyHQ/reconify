//nolint:staticcheck // Domain aliases keep package-internal signatures readable.
package parser

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/reconifyhq/reconify/config"
	. "github.com/reconifyhq/reconify/engine/domain"
	"github.com/xuri/excelize/v2"
)

func parseXLSXEach(
	ctx context.Context,
	sourceName string,
	filePath string,
	cfg config.ParserCfg,
	fn func(tx Transaction, rowNum int) error,
) error {
	if strings.EqualFold(filepath.Ext(filePath), ".xls") {
		return fmt.Errorf("unsupported Excel format %q: legacy .xls files are not supported; save as .xlsx or .csv", filePath)
	}

	f, err := excelize.OpenFile(filePath)
	if err != nil {
		return fmt.Errorf("open %q: %w", filePath, err)
	}
	defer func() {
		_ = f.Close()
	}()

	sheet, err := resolveSheet(f, cfg)
	if err != nil {
		return fmt.Errorf("%s: %w", filePath, err)
	}

	rows, err := f.Rows(sheet)
	if err != nil {
		return fmt.Errorf("%s: sheet %q: %w", filePath, sheet, err)
	}
	defer func() {
		_ = rows.Close()
	}()

	if !rows.Next() {
		if err := rows.Error(); err != nil {
			return fmt.Errorf("%s: sheet %q: read header: %w", filePath, sheet, err)
		}
		return fmt.Errorf("%s: sheet %q: read header: %w", filePath, sheet, io.EOF)
	}
	headers, err := rows.Columns()
	if err != nil {
		return fmt.Errorf("%s: sheet %q: read header: %w", filePath, sheet, err)
	}

	normalizer := newRowNormalizer(sourceName, filePath, cfg)
	rowNum := 0
	for rows.Next() {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		record, err := rows.Columns()
		if err != nil {
			return fmt.Errorf("%s: sheet %q: row %d: %w", filePath, sheet, rowNum+2, err)
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
	if err := rows.Error(); err != nil {
		return fmt.Errorf("%s: sheet %q: %w", filePath, sheet, err)
	}

	return nil
}
func readXLSXHeaders(ctx context.Context, filePath string, cfg config.ParserCfg) ([]string, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if strings.EqualFold(filepath.Ext(filePath), ".xls") {
		return nil, fmt.Errorf("unsupported Excel format %q: legacy .xls files are not supported; save as .xlsx or .csv", filePath)
	}
	f, err := excelize.OpenFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("open %q: %w", filePath, err)
	}
	defer func() {
		_ = f.Close()
	}()
	sheet, err := resolveSheet(f, cfg)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filePath, err)
	}
	rows, err := f.Rows(sheet)
	if err != nil {
		return nil, fmt.Errorf("%s: sheet %q: %w", filePath, sheet, err)
	}
	defer func() {
		_ = rows.Close()
	}()
	if !rows.Next() {
		if err := rows.Error(); err != nil {
			return nil, fmt.Errorf("%s: sheet %q: read header: %w", filePath, sheet, err)
		}
		return nil, fmt.Errorf("%s: sheet %q: read header: %w", filePath, sheet, io.EOF)
	}
	headers, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("%s: sheet %q: read header: %w", filePath, sheet, err)
	}
	return headers, nil
}

// openXLSXRows opens filePath, resolves the target sheet, and returns its
// header row plus a live *excelize.Rows cursor positioned after the header.
// The caller must invoke the returned close function (which closes both the
// row cursor and the workbook) when done.
func openXLSXRows(filePath string, cfg config.ParserCfg) (headers []string, rows *excelize.Rows, closeFn func(), err error) {
	if strings.EqualFold(filepath.Ext(filePath), ".xls") {
		return nil, nil, nil, fmt.Errorf("unsupported Excel format %q: legacy .xls files are not supported; save as .xlsx or .csv", filePath)
	}

	f, err := excelize.OpenFile(filePath)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("open %q: %w", filePath, err)
	}

	sheet, err := resolveSheet(f, cfg)
	if err != nil {
		_ = f.Close()
		return nil, nil, nil, fmt.Errorf("%s: %w", filePath, err)
	}

	r, err := f.Rows(sheet)
	if err != nil {
		_ = f.Close()
		return nil, nil, nil, fmt.Errorf("%s: sheet %q: %w", filePath, sheet, err)
	}

	if !r.Next() {
		if err := r.Error(); err != nil {
			_ = r.Close()
			_ = f.Close()
			return nil, nil, nil, fmt.Errorf("%s: sheet %q: read header: %w", filePath, sheet, err)
		}
		_ = r.Close()
		_ = f.Close()
		return nil, nil, nil, fmt.Errorf("%s: sheet %q: read header: %w", filePath, sheet, io.EOF)
	}
	h, err := r.Columns()
	if err != nil {
		_ = r.Close()
		_ = f.Close()
		return nil, nil, nil, fmt.Errorf("%s: sheet %q: read header: %w", filePath, sheet, err)
	}

	return h, r, func() {
		_ = r.Close()
		_ = f.Close()
	}, nil
}

func resolveSheet(f *excelize.File, cfg config.ParserCfg) (string, error) {
	if cfg.Sheet != "" {
		return cfg.Sheet, nil
	}
	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return "", fmt.Errorf("workbook has no sheets")
	}
	return sheets[0], nil
}
