//nolint:staticcheck // Domain aliases keep package-internal signatures readable.
package parser

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/reconifyhq/reconify/config"
	. "github.com/reconifyhq/reconify/engine/domain"
	"github.com/xuri/excelize/v2"
)

func TestParseCSVEach_WideSchema_Valid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wide.csv")
	content := strings.Join([]string{
		"date,description,amount,ref_id,currency,account_id,customer_id,branch_code,txn_type,status,channel,country,batch_id,line_no,value_date,booking_ts,balance_after_minor,processor_hint",
		`2024-01-31,"Invoice 123, Retail Segment","1,234.56",REF-001,USD,ACCT-1,CUST-1,NYC01,debit,posted,api,US,BATCH-1,1,2024-01-30,2024-01-31T09:31:00Z,100000,fixture`,
		`2024-02-01,"Chargeback, Card Present","(45.10)",REF-002,USD,ACCT-2,CUST-2,LON02,credit,cleared,mobile,GB,BATCH-1,2,2024-01-31,2024-02-01T09:31:00Z,99955,fixture`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := config.CSVParserCfg{
		Type:        "csv",
		DateCol:     "date",
		DateLayout:  "2006-01-02",
		AmountCol:   "amount",
		Decimal:     ".",
		Thousands:   ",",
		Multiplier:  100,
		CurrencyCol: "currency",
		RefCol:      "ref_id",
		NameCol:     "description",
	}

	var got []Transaction
	err := ParseCSVEach(context.Background(), "bank", path, cfg, func(tx Transaction, _ int) error {
		got = append(got, tx)
		return nil
	})
	if err != nil {
		t.Fatalf("ParseCSVEach error: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("len(got)=%d, want 2", len(got))
	}
	if got[0].Amount != 123456 {
		t.Errorf("row1 amount=%d, want 123456", got[0].Amount)
	}
	if got[1].Amount != -4510 {
		t.Errorf("row2 amount=%d, want -4510", got[1].Amount)
	}
	if got[0].Reference != "REF-001" || got[1].Reference != "REF-002" {
		t.Errorf("unexpected refs: %q %q", got[0].Reference, got[1].Reference)
	}
	if len(got[0].Raw) < 10 {
		t.Errorf("expected raw map with many fields, got %+v", got[0].Raw)
	}
}

func TestParseCSVEach_SkipRaw(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wide.csv")
	content := "date,description,amount,ref_id,currency,account_id,customer_id,branch_code,txn_type,status,channel,country\n" +
		"2024-01-31,Payment A,100.00,REF-001,USD,ACCT-1,CUST-1,NYC01,debit,posted,api,US\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := config.CSVParserCfg{
		Type:        "csv",
		DateCol:     "date",
		DateLayout:  "2006-01-02",
		AmountCol:   "amount",
		Decimal:     ".",
		Multiplier:  100,
		CurrencyCol: "currency",
		RefCol:      "ref_id",
		NameCol:     "description",
		SkipRaw:     true,
	}

	var one Transaction
	err := ParseCSVEach(context.Background(), "bank", path, cfg, func(tx Transaction, _ int) error {
		one = tx
		return nil
	})
	if err != nil {
		t.Fatalf("ParseCSVEach error: %v", err)
	}
	if one.Raw != nil {
		t.Fatalf("expected Raw=nil when skip_raw=true, got %+v", one.Raw)
	}
}

func TestParseCSVEach_NormalizesFinancialFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "financial.csv")
	if err := os.WriteFile(path, []byte("date,amount,gross,fee\n2024-01-01,98.50,100.00,1.50\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.CSVParserCfg{Type: "csv", DateCol: "date", DateLayout: "2006-01-02", AmountCol: "amount", Multiplier: 100,
		Financials: &config.FinancialsCfg{GrossCol: "gross", Fields: map[string]string{"fee": "fee"}}}
	var got Transaction
	rows, err := Parse("bank", path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	got = rows[0]
	if got.Amount != 9850 || got.Financials["gross"] != 10000 || got.Financials["fee"] != 150 {
		t.Fatalf("normalized values = %#v amount=%d", got.Financials, got.Amount)
	}
}

func TestParseAmount_Precision(t *testing.T) {
	tests := []struct {
		name       string
		amount     string
		decimal    string
		thousands  string
		multiplier int64
		want       int64
	}{
		{
			name:       "large integer beyond float64 53-bit mantissa",
			amount:     "9007199254740993", // 2^53 + 1, not exactly representable as float64
			decimal:    ".",
			multiplier: 1,
			want:       9007199254740993,
		},
		{
			name:       "large amount with cents beyond float64 precision",
			amount:     "123456789012345.67",
			decimal:    ".",
			multiplier: 100,
			want:       12345678901234567,
		},
		{
			name:       "ordinary cents rounding",
			amount:     "19.995",
			decimal:    ".",
			multiplier: 100,
			want:       2000, // rounds half away from zero
		},
		{
			name:       "negative with parentheses-stripped sign",
			amount:     "-1234.56",
			decimal:    ".",
			multiplier: 100,
			want:       -123456,
		},
		{
			name:       "thousands separator removed",
			amount:     "1,234,567.89",
			decimal:    ".",
			thousands:  ",",
			multiplier: 100,
			want:       123456789,
		},
		{
			// fracVal*multiplier (999999999999999999 * 100 ≈ 1e20) overflows int64
			// (~9.22e18) if computed with plain int64 multiplication. The fix uses
			// math/bits 128-bit multiply/divide so this rounds correctly instead of
			// silently wrapping to a wrong (and wrong-signed) amount.
			name:       "18 fractional digits does not overflow int64 multiply",
			amount:     "0.999999999999999999",
			decimal:    ".",
			multiplier: 100,
			want:       100, // rounds up to 1.00 in minor units
		},
		{
			name:       "positive int64 boundary",
			amount:     "9223372036854775807",
			decimal:    ".",
			multiplier: 1,
			want:       9223372036854775807,
		},
		{
			name:       "negative int64 boundary",
			amount:     "-9223372036854775808",
			decimal:    ".",
			multiplier: 1,
			want:       -9223372036854775808,
		},
		{
			name:       "positive fractional boundary carry",
			amount:     "92233720368547758.07",
			decimal:    ".",
			multiplier: 100,
			want:       9223372036854775807,
		},
		{
			name:       "negative fractional boundary carry",
			amount:     "-92233720368547758.08",
			decimal:    ".",
			multiplier: 100,
			want:       -9223372036854775808,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseAmount(tc.amount, tc.decimal, tc.thousands, tc.multiplier)
			if err != nil {
				t.Fatalf("parseAmount(%q) error: %v", tc.amount, err)
			}
			if got != tc.want {
				t.Errorf("parseAmount(%q) = %d, want %d", tc.amount, got, tc.want)
			}
		})
	}
}

func TestParseAmount_RejectsOverflow(t *testing.T) {
	tests := []struct {
		name       string
		amount     string
		multiplier int64
	}{
		{name: "positive boundary overflow", amount: "9223372036854775808", multiplier: 1},
		{name: "negative boundary overflow", amount: "-9223372036854775809", multiplier: 1},
		{name: "whole-number multiplication", amount: "2", multiplier: 9223372036854775807},
		{name: "fractional addition", amount: "92233720368547758.08", multiplier: 100},
		{name: "negative fractional addition", amount: "-92233720368547758.09", multiplier: 100},
		{name: "invalid multiplier", amount: "1", multiplier: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseAmount(tc.amount, ".", "", tc.multiplier); err == nil || !strings.Contains(err.Error(), "overflow") && !strings.Contains(err.Error(), "multiplier") {
				t.Fatalf("parseAmount(%q, multiplier=%d) error = %v, want overflow/multiplier error", tc.amount, tc.multiplier, err)
			}
		})
	}
}

func TestParseCSVEach_AmountOverflowIncludesRowContext(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "overflow.csv")
	content := "date,amount,reference\n2024-01-01,9223372036854775808,REF-1\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := ParseCSV("ledger", path, config.CSVParserCfg{
		Type: "csv", DateCol: "date", DateLayout: "2006-01-02", AmountCol: "amount",
		Multiplier: 1, RefCol: "reference",
	})
	if err == nil {
		t.Fatal("expected amount overflow")
	}
	for _, want := range []string{path, "row 2", `source "ledger"`, "amount overflow"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

func TestParseCSVEach_DirtyDataErrors(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name: "invalid date",
			content: "date,description,amount,ref_id,currency\n" +
				"2024-99-99,Payment A,100.00,REF-1,USD\n",
			want: "invalid date",
		},
		{
			name: "empty date",
			content: "date,description,amount,ref_id,currency\n" +
				",Payment A,100.00,REF-1,USD\n",
			want: "date column",
		},
		{
			name: "bad amount",
			content: "date,description,amount,ref_id,currency\n" +
				"2024-01-01,Payment A,12O0.50,REF-1,USD\n",
			want: "parse amount",
		},
		{
			name: "empty amount",
			content: "date,description,amount,ref_id,currency\n" +
				"2024-01-01,Payment A,,REF-1,USD\n",
			want: "amount column",
		},
		{
			name: "malformed csv",
			content: "date,description,amount,ref_id,currency\n" +
				"2024-01-01,\"broken,100.00,REF-1,USD\n",
			want: "row",
		},
	}

	cfg := config.CSVParserCfg{
		Type:        "csv",
		DateCol:     "date",
		DateLayout:  "2006-01-02",
		AmountCol:   "amount",
		Decimal:     ".",
		Multiplier:  100,
		CurrencyCol: "currency",
		RefCol:      "ref_id",
		NameCol:     "description",
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "input.csv")
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}

			err := ParseCSVEach(context.Background(), "bank", path, cfg, func(_ Transaction, _ int) error {
				return nil
			})
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.want)
			}
		})
	}
}

func TestParseEach_JSONArray(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "input.json")
	content := `[
		{"date":"2024-01-31","description":"Payment A","amount":"123.45","ref_id":"REF-001","currency":"USD"},
		{"date":"2024-02-01","description":"Payment B","amount":"2.50","ref_id":"REF-002","currency":"USD"}
	]`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := baseParserCfg("json")
	got, err := Parse("bank", path, cfg)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got)=%d, want 2", len(got))
	}
	if got[0].Amount != 12345 || got[1].Amount != 250 {
		t.Fatalf("amounts=%d,%d want 12345,250", got[0].Amount, got[1].Amount)
	}
	if got[0].Reference != "REF-001" || got[0].Name != "Payment A" {
		t.Fatalf("unexpected row: %+v", got[0])
	}
	if got[0].Raw["description"] != "Payment A" {
		t.Fatalf("expected raw JSON fields, got %+v", got[0].Raw)
	}
}

func TestParseEach_JSONArrayRejectsTrailingContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "input.json")
	content := `[
		{"date":"2024-01-31","description":"Payment A","amount":"123.45","ref_id":"REF-001","currency":"USD"}
	] {"date":"2024-02-01","description":"Payment B","amount":"2.50","ref_id":"REF-002","currency":"USD"}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Parse("bank", path, baseParserCfg("json"))
	if err == nil {
		t.Fatal("expected trailing JSON error, got nil")
	}
	if !strings.Contains(err.Error(), "trailing JSON after array") {
		t.Fatalf("error %q does not contain trailing JSON message", err.Error())
	}
}

func TestParseEach_NDJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "input.ndjson")
	content := strings.Join([]string{
		`{"date":"2024-01-31","description":"Payment A","amount":1.25,"ref_id":"REF-001","currency":"USD"}`,
		`{"date":"2024-02-01","description":"Payment B","amount":2.5,"ref_id":"REF-002","currency":"USD"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	var got []Transaction
	if err := ParseEach(context.Background(), "bank", path, baseParserCfg("json"), func(tx Transaction, _ int) error {
		got = append(got, tx)
		return nil
	}); err != nil {
		t.Fatalf("ParseEach error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got)=%d, want 2", len(got))
	}
	if got[0].Amount != 125 || got[1].Amount != 250 {
		t.Fatalf("amounts=%d,%d want 125,250", got[0].Amount, got[1].Amount)
	}
}

func TestParseEach_XLSXFirstSheetDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "input.xlsx")
	writeWorkbook(t, path, "Sheet1", [][]string{
		{"date", "description", "amount", "ref_id", "currency"},
		{"2024-01-31", "Payment A", "12.34", "REF-001", "USD"},
	})

	got, err := Parse("bank", path, baseParserCfg("xlsx"))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got)=%d, want 1", len(got))
	}
	if got[0].Amount != 1234 || got[0].Reference != "REF-001" {
		t.Fatalf("unexpected row: %+v", got[0])
	}
}

func TestParseEach_XLSXConfiguredSheet(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "input.xlsx")
	f := excelize.NewFile()
	defer func() {
		_ = f.Close()
	}()
	_, err := f.NewSheet("Transactions")
	if err != nil {
		t.Fatal(err)
	}
	setRows(t, f, "Sheet1", [][]string{
		{"wrong", "columns"},
		{"ignored", "row"},
	})
	setRows(t, f, "Transactions", [][]string{
		{"date", "description", "amount", "ref_id", "currency"},
		{"2024-02-01", "Payment B", "5.67", "REF-002", "USD"},
	})
	if err := f.SaveAs(path); err != nil {
		t.Fatal(err)
	}

	cfg := baseParserCfg("xlsx")
	cfg.Sheet = "Transactions"
	got, err := Parse("bank", path, cfg)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(got) != 1 || got[0].Reference != "REF-002" || got[0].Amount != 567 {
		t.Fatalf("unexpected rows: %+v", got)
	}
}

func TestParseEach_AutoDetectFormats(t *testing.T) {
	dir := t.TempDir()
	csvPath := filepath.Join(dir, "input.csv")
	jsonPath := filepath.Join(dir, "input.json")
	xlsxPath := filepath.Join(dir, "input.xlsx")

	if err := os.WriteFile(csvPath, []byte("date,description,amount,ref_id,currency\n2024-01-31,A,1.00,REF-C,USD\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jsonPath, []byte(`{"date":"2024-01-31","description":"B","amount":"2.00","ref_id":"REF-J","currency":"USD"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	writeWorkbook(t, xlsxPath, "Sheet1", [][]string{
		{"date", "description", "amount", "ref_id", "currency"},
		{"2024-01-31", "C", "3.00", "REF-X", "USD"},
	})

	cfg := baseParserCfg("auto")
	for _, path := range []string{csvPath, jsonPath, xlsxPath} {
		got, err := Parse("bank", path, cfg)
		if err != nil {
			t.Fatalf("Parse(%s) error: %v", filepath.Base(path), err)
		}
		if len(got) != 1 {
			t.Fatalf("Parse(%s) len=%d, want 1", filepath.Base(path), len(got))
		}
	}
}

func TestParseEach_UnsupportedXLS(t *testing.T) {
	_, err := Parse("bank", "legacy.xls", baseParserCfg("auto"))
	if err == nil {
		t.Fatal("expected unsupported .xls error, got nil")
	}
	if !strings.Contains(err.Error(), "legacy .xls files are not supported") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseEach_JSONDirtyDataErrorIncludesRowSourceAndValue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "input.json")
	if err := os.WriteFile(path, []byte(`[{"date":"2024-99-99","description":"A","amount":"1.00","ref_id":"REF","currency":"USD"}]`), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Parse("bank", path, baseParserCfg("json"))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	for _, want := range []string{path, "row 1", `source "bank"`, "2024-99-99"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not contain %q", err.Error(), want)
		}
	}
}

func baseParserCfg(parserType string) config.ParserCfg {
	return config.ParserCfg{
		Type:        parserType,
		DateCol:     "date",
		DateLayout:  "2006-01-02",
		AmountCol:   "amount",
		Decimal:     ".",
		Thousands:   ",",
		Multiplier:  100,
		CurrencyCol: "currency",
		RefCol:      "ref_id",
		NameCol:     "description",
	}
}

func writeWorkbook(t *testing.T, path, sheet string, rows [][]string) {
	t.Helper()
	f := excelize.NewFile()
	defer func() {
		_ = f.Close()
	}()
	if sheet != "Sheet1" {
		if _, err := f.NewSheet(sheet); err != nil {
			t.Fatal(err)
		}
	}
	setRows(t, f, sheet, rows)
	if err := f.SaveAs(path); err != nil {
		t.Fatal(err)
	}
}

func setRows(t *testing.T, f *excelize.File, sheet string, rows [][]string) {
	t.Helper()
	for rowIdx, row := range rows {
		for colIdx, val := range row {
			cell, err := excelize.CoordinatesToCellName(colIdx+1, rowIdx+1)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.SetCellValue(sheet, cell, val); err != nil {
				t.Fatal(err)
			}
		}
	}
}
