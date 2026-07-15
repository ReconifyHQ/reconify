//nolint:staticcheck // Domain aliases keep package-internal signatures readable.
package parser

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"unicode"

	"github.com/reconifyhq/reconify/config"
	. "github.com/reconifyhq/reconify/engine/domain"
)

func parseJSONEach(
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

	br := bufio.NewReaderSize(f, 1<<20)
	first, err := peekFirstNonSpace(br)
	if err != nil {
		return fmt.Errorf("%s: read JSON: %w", filePath, err)
	}

	dec := json.NewDecoder(br)
	dec.UseNumber()
	normalizer := newRowNormalizer(sourceName, filePath, cfg)
	rowNum := 0

	if first == '[' {
		token, err := dec.Token()
		if err != nil {
			return fmt.Errorf("%s: read JSON array: %w", filePath, err)
		}
		if delim, ok := token.(json.Delim); !ok || delim != '[' {
			return fmt.Errorf("%s: expected JSON array", filePath)
		}
		for dec.More() {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			var obj map[string]any
			if err := dec.Decode(&obj); err != nil {
				return fmt.Errorf("%s: row %d: decode JSON object: %w", filePath, rowNum+1, err)
			}
			rowNum++
			tx, err := normalizer.fromMap(jsonObjectToStrings(obj), rowNum, rowNum)
			if err != nil {
				return err
			}
			if err := fn(tx, rowNum); err != nil {
				return err
			}
		}
		if _, err := dec.Token(); err != nil {
			return fmt.Errorf("%s: close JSON array: %w", filePath, err)
		}
		if _, err := dec.Token(); err != io.EOF {
			if err != nil {
				return fmt.Errorf("%s: trailing JSON after array: %w", filePath, err)
			}
			return fmt.Errorf("%s: trailing JSON after array", filePath)
		}
		return nil
	}

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var obj map[string]any
		err := dec.Decode(&obj)
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("%s: row %d: decode JSON object: %w", filePath, rowNum+1, err)
		}
		rowNum++
		tx, err := normalizer.fromMap(jsonObjectToStrings(obj), rowNum, rowNum)
		if err != nil {
			return err
		}
		if err := fn(tx, rowNum); err != nil {
			return err
		}
	}

	return nil
}
func readJSONHeaders(ctx context.Context, filePath string) ([]string, error) {
	f, err := os.Open(filePath) // #nosec G304 -- parser input paths are explicit CLI/config/user-selected files.
	if err != nil {
		return nil, fmt.Errorf("open %q: %w", filePath, err)
	}
	defer func() {
		_ = f.Close()
	}()

	br := bufio.NewReaderSize(f, 1<<20)
	first, err := peekFirstNonSpace(br)
	if err != nil {
		return nil, fmt.Errorf("%s: read JSON: %w", filePath, err)
	}

	dec := json.NewDecoder(br)
	dec.UseNumber()
	if first == '[' {
		if _, err := dec.Token(); err != nil {
			return nil, fmt.Errorf("%s: read JSON array: %w", filePath, err)
		}
		if !dec.More() {
			return nil, fmt.Errorf("%s: JSON array is empty", filePath)
		}
	}

	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	var obj map[string]any
	if err := dec.Decode(&obj); err != nil {
		return nil, fmt.Errorf("%s: decode JSON object: %w", filePath, err)
	}
	headers := make([]string, 0, len(obj))
	for k := range obj {
		headers = append(headers, k)
	}
	sort.Strings(headers)
	return headers, nil
}
func jsonObjectToStrings(obj map[string]any) map[string]string {
	values := make(map[string]string, len(obj))
	for k, v := range obj {
		values[k] = jsonValueToString(v)
	}
	return values
}

func jsonValueToString(v any) string {
	switch val := v.(type) {
	case nil:
		return ""
	case string:
		return val
	case json.Number:
		return val.String()
	case bool:
		return strconv.FormatBool(val)
	default:
		b, err := json.Marshal(val)
		if err != nil {
			return fmt.Sprint(val)
		}
		return string(b)
	}
}

func peekFirstNonSpace(r *bufio.Reader) (byte, error) {
	for {
		b, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		if !unicode.IsSpace(rune(b)) {
			if err := r.UnreadByte(); err != nil {
				return 0, err
			}
			return b, nil
		}
	}
}
