//nolint:staticcheck // Domain aliases keep package-internal signatures readable.
package parser

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/reconifyhq/reconify/config"
)

// RawRowsEach streams raw, unmapped rows from a supported input file: no
// column mapping, date parsing, or amount parsing is applied. It exists for
// callers (such as file profiling) that need to inspect a file before a
// column mapping config exists.
//
// limit bounds the number of rows passed to fn; limit <= 0 means unbounded.
// When limit > 0, RawRowsEach attempts to read exactly one row beyond the
// limit (without passing it to fn) to determine whether the file contains
// more rows than were scanned; truncated reports that.
func RawRowsEach(
	ctx context.Context,
	filePath string,
	cfg config.ParserCfg,
	limit int,
	fn func(row map[string]string, rowNum int) error,
) (headers []string, truncated bool, err error) {
	parserType, err := ResolveParserType(filePath, cfg)
	if err != nil {
		return nil, false, err
	}

	switch parserType {
	case "csv":
		return rawCSVRowsEach(ctx, filePath, limit, fn)
	case "json":
		return rawJSONRowsEach(ctx, filePath, limit, fn)
	case "xlsx":
		return rawXLSXRowsEach(ctx, filePath, cfg, limit, fn)
	default:
		return nil, false, fmt.Errorf("unsupported parser type %q", parserType)
	}
}

func rawCSVRowsEach(ctx context.Context, filePath string, limit int, fn func(row map[string]string, rowNum int) error) ([]string, bool, error) {
	f, err := os.Open(filePath) // #nosec G304 -- parser input paths are explicit CLI/config/user-selected files.
	if err != nil {
		return nil, false, fmt.Errorf("open %q: %w", filePath, err)
	}
	defer func() {
		_ = f.Close()
	}()

	r := csv.NewReader(bufio.NewReaderSize(f, 1<<20))
	r.TrimLeadingSpace = true

	headers, err := r.Read()
	if err != nil {
		return nil, false, fmt.Errorf("%s: read header: %w", filePath, err)
	}

	rowNum := 0
	for {
		if ctx.Err() != nil {
			return headers, false, ctx.Err()
		}
		record, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return headers, false, fmt.Errorf("%s: row %d: %w", filePath, rowNum+2, err)
		}
		rowNum++
		if limit > 0 && rowNum > limit {
			return headers, true, nil
		}
		if err := fn(recordToRawRow(headers, record), rowNum); err != nil {
			return headers, false, err
		}
	}

	return headers, false, nil
}

func recordToRawRow(headers, record []string) map[string]string {
	values := make(map[string]string, len(headers))
	for i, h := range headers {
		if i < len(record) {
			values[h] = record[i]
		} else {
			values[h] = ""
		}
	}
	return values
}

func rawJSONRowsEach(ctx context.Context, filePath string, limit int, fn func(row map[string]string, rowNum int) error) ([]string, bool, error) {
	f, err := os.Open(filePath) // #nosec G304 -- parser input paths are explicit CLI/config/user-selected files.
	if err != nil {
		return nil, false, fmt.Errorf("open %q: %w", filePath, err)
	}
	defer func() {
		_ = f.Close()
	}()

	br := bufio.NewReaderSize(f, 1<<20)
	first, err := peekFirstNonSpace(br)
	if err != nil {
		return nil, false, fmt.Errorf("%s: read JSON: %w", filePath, err)
	}

	dec := json.NewDecoder(br)
	dec.UseNumber()

	var headers []string
	rowNum := 0
	decodeOne := func() (map[string]string, bool, error) {
		var obj map[string]any
		err := dec.Decode(&obj)
		if err == io.EOF {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, fmt.Errorf("%s: row %d: decode JSON object: %w", filePath, rowNum+1, err)
		}
		return jsonObjectToStrings(obj), true, nil
	}

	if first == '[' {
		token, err := dec.Token()
		if err != nil {
			return nil, false, fmt.Errorf("%s: read JSON array: %w", filePath, err)
		}
		if delim, ok := token.(json.Delim); !ok || delim != '[' {
			return nil, false, fmt.Errorf("%s: expected JSON array", filePath)
		}
		for dec.More() {
			if ctx.Err() != nil {
				return headers, false, ctx.Err()
			}
			row, ok, err := decodeOne()
			if err != nil {
				return headers, false, err
			}
			if !ok {
				break
			}
			rowNum++
			if headers == nil {
				headers = rawRowKeys(row)
			}
			if limit > 0 && rowNum > limit {
				return headers, true, nil
			}
			if err := fn(row, rowNum); err != nil {
				return headers, false, err
			}
		}
		if _, err := dec.Token(); err != nil {
			return headers, false, fmt.Errorf("%s: close JSON array: %w", filePath, err)
		}
		if _, err := dec.Token(); err != io.EOF {
			if err != nil {
				return headers, false, fmt.Errorf("%s: trailing JSON after array: %w", filePath, err)
			}
			return headers, false, fmt.Errorf("%s: trailing JSON after array", filePath)
		}
		return headers, false, nil
	}

	for {
		if ctx.Err() != nil {
			return headers, false, ctx.Err()
		}
		row, ok, err := decodeOne()
		if err != nil {
			return headers, false, err
		}
		if !ok {
			break
		}
		rowNum++
		if headers == nil {
			headers = rawRowKeys(row)
		}
		if limit > 0 && rowNum > limit {
			return headers, true, nil
		}
		if err := fn(row, rowNum); err != nil {
			return headers, false, err
		}
	}

	return headers, false, nil
}

func rawXLSXRowsEach(ctx context.Context, filePath string, cfg config.ParserCfg, limit int, fn func(row map[string]string, rowNum int) error) ([]string, bool, error) {
	headers, rows, closeFn, err := openXLSXRows(filePath, cfg)
	if err != nil {
		return nil, false, err
	}
	defer closeFn()

	rowNum := 0
	for rows.Next() {
		if ctx.Err() != nil {
			return headers, false, ctx.Err()
		}
		record, err := rows.Columns()
		if err != nil {
			return headers, false, fmt.Errorf("%s: row %d: %w", filePath, rowNum+2, err)
		}
		rowNum++
		if limit > 0 && rowNum > limit {
			return headers, true, nil
		}
		if err := fn(recordToRawRow(headers, record), rowNum); err != nil {
			return headers, false, err
		}
	}
	if err := rows.Error(); err != nil {
		return headers, false, err
	}

	return headers, false, nil
}

func rawRowKeys(row map[string]string) []string {
	headers := make([]string, 0, len(row))
	for k := range row {
		headers = append(headers, k)
	}
	sort.Strings(headers)
	return headers
}
