package engine

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math/bits"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/reconifyhq/reconify/config"
	"github.com/xuri/excelize/v2"
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

func parseDelimitedEach(
	ctx context.Context,
	sourceName string,
	filePath string,
	cfg config.ParserCfg,
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

func parseJSONEach(
	ctx context.Context,
	sourceName string,
	filePath string,
	cfg config.ParserCfg,
	fn func(tx Transaction, rowNum int) error,
) error {
	f, err := os.Open(filePath)
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

type rowNormalizer struct {
	sourceName   string
	filePath     string
	cfg          config.ParserCfg
	loc          *time.Location
	decimal      string
	multiplier   int64
	dateCache    map[string]time.Time
	cacheEnabled bool
}

func newRowNormalizer(sourceName, filePath string, cfg config.ParserCfg) *rowNormalizer {
	loc := time.UTC
	if cfg.TZ != "" {
		if l, err := time.LoadLocation(cfg.TZ); err == nil {
			loc = l
		}
	}

	decimal := "."
	if cfg.Decimal != "" {
		decimal = cfg.Decimal
	}

	multiplier := cfg.Multiplier
	if multiplier <= 0 {
		multiplier = 1
	}

	return &rowNormalizer{
		sourceName:   sourceName,
		filePath:     filePath,
		cfg:          cfg,
		loc:          loc,
		decimal:      decimal,
		multiplier:   multiplier,
		dateCache:    make(map[string]time.Time),
		cacheEnabled: true,
	}
}

func (n *rowNormalizer) fromRecord(headers, record []string, idRowNum, fileRowNum int) (Transaction, error) {
	values := make(map[string]string, len(headers))
	for i, h := range headers {
		if i < len(record) {
			values[h] = record[i]
		} else {
			values[h] = ""
		}
	}
	return n.fromMap(values, idRowNum, fileRowNum)
}

func (n *rowNormalizer) fromMap(values map[string]string, idRowNum, fileRowNum int) (Transaction, error) {
	dateStr := strings.TrimSpace(getMapCol(values, n.cfg.DateCol))
	if dateStr == "" {
		return Transaction{}, fmt.Errorf("%s: row %d: source %q: date column %q is empty",
			n.filePath, fileRowNum, n.sourceName, n.cfg.DateCol)
	}

	var date time.Time
	if n.cacheEnabled {
		if t, ok := n.dateCache[dateStr]; ok {
			date = t
		} else {
			t, parseErr := time.ParseInLocation(n.cfg.DateLayout, dateStr, n.loc)
			if parseErr != nil {
				return Transaction{}, fmt.Errorf("%s: row %d: source %q: invalid date %q with layout %q: %w",
					n.filePath, fileRowNum, n.sourceName, dateStr, n.cfg.DateLayout, parseErr)
			}
			date = t
			if len(n.dateCache) >= 1000 {
				n.cacheEnabled = false
				n.dateCache = nil
			} else {
				n.dateCache[dateStr] = t
			}
		}
	} else {
		t, parseErr := time.ParseInLocation(n.cfg.DateLayout, dateStr, n.loc)
		if parseErr != nil {
			return Transaction{}, fmt.Errorf("%s: row %d: source %q: invalid date %q with layout %q: %w",
				n.filePath, fileRowNum, n.sourceName, dateStr, n.cfg.DateLayout, parseErr)
		}
		date = t
	}

	amtStr := getMapCol(values, n.cfg.AmountCol)
	if amtStr == "" {
		return Transaction{}, fmt.Errorf("%s: row %d: source %q: amount column %q is empty",
			n.filePath, fileRowNum, n.sourceName, n.cfg.AmountCol)
	}
	amount, err := parseAmount(amtStr, n.decimal, n.cfg.Thousands, n.multiplier)
	if err != nil {
		return Transaction{}, fmt.Errorf("%s: row %d: source %q: parse amount %q: %w",
			n.filePath, fileRowNum, n.sourceName, amtStr, err)
	}

	var raw map[string]string
	if !n.cfg.SkipRaw {
		raw = make(map[string]string, len(values))
		for k, v := range values {
			raw[k] = v
		}
	}

	reference := strings.TrimSpace(getMapCol(values, n.cfg.RefCol))
	groupKey := reference
	if n.cfg.GroupCol != "" {
		groupKey = strings.TrimSpace(getMapCol(values, n.cfg.GroupCol))
	}

	return Transaction{
		ID:        fmt.Sprintf("%s-%d", n.sourceName, idRowNum),
		Date:      date,
		Amount:    amount,
		Source:    n.sourceName,
		Raw:       raw,
		Currency:  strings.TrimSpace(getMapCol(values, n.cfg.CurrencyCol)),
		Reference: reference,
		Name:      strings.TrimSpace(getMapCol(values, n.cfg.NameCol)),
		GroupKey:  groupKey,
	}, nil
}

func readCSVHeaders(filePath string) ([]string, error) {
	f, err := os.Open(filePath)
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

func readJSONHeaders(ctx context.Context, filePath string) ([]string, error) {
	f, err := os.Open(filePath)
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

func getMapCol(values map[string]string, colName string) string {
	if colName == "" {
		return ""
	}
	want := strings.ToLower(strings.TrimSpace(colName))
	for k, v := range values {
		if strings.ToLower(strings.TrimSpace(k)) == want {
			return v
		}
	}
	return ""
}

// parseAmount parses an amount string to int64 minor units.
// It removes the thousands separator, normalizes the decimal separator to ".",
// then parses using integer/string arithmetic (no float64 round-trip) to avoid
// precision loss on large or high-precision amounts.
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

	// Handle parentheses for negative amounts: (1234.56) -> -1234.56
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		s = "-" + s[1:len(s)-1]
	}

	// Remove any currency symbols or spaces that might remain
	s = strings.TrimSpace(s)

	if s == "" {
		return 0, fmt.Errorf("not a number: %q", s)
	}

	negative := false
	switch s[0] {
	case '-':
		negative = true
		s = s[1:]
	case '+':
		s = s[1:]
	}
	if s == "" {
		return 0, fmt.Errorf("not a number: %q", s)
	}

	intPart, fracPart := s, ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		intPart, fracPart = s[:i], s[i+1:]
		if strings.ContainsRune(fracPart, '.') {
			return 0, fmt.Errorf("not a number: %q", s)
		}
	}
	if intPart == "" {
		intPart = "0"
	}
	if len(fracPart) > 18 {
		return 0, fmt.Errorf("not a number: %q", s)
	}

	intVal, err := strconv.ParseInt(intPart, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("not a number: %q", s)
	}

	var fracScaled int64
	if fracPart != "" {
		fracVal, err := strconv.ParseInt(fracPart, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("not a number: %q", s)
		}
		denom := int64(1)
		for i := 0; i < len(fracPart); i++ {
			denom *= 10
		}
		// fracVal*multiplier can exceed int64 (e.g. 18 fractional digits with a
		// multiplier >= 100), so do the multiply/divide as a 128-bit operation via
		// math/bits rather than int64 arithmetic that would silently wrap. The true
		// quotient is always < multiplier (fracVal < denom by construction), so it
		// fits safely back into int64/uint64 once divided.
		hi, lo := bits.Mul64(uint64(fracVal), uint64(multiplier))
		q, r := bits.Div64(hi, lo, uint64(denom))
		if 2*r >= uint64(denom) {
			q++
		}
		fracScaled = int64(q)
	}

	amount := intVal*multiplier + fracScaled
	if negative {
		amount = -amount
	}
	return amount, nil
}
