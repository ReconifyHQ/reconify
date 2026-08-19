package profile

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/reconifyhq/reconify/config"
	"github.com/reconifyhq/reconify/schemas"
	"github.com/xuri/excelize/v2"
)

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeWorkbook(t *testing.T, path string, rows [][]string) {
	t.Helper()
	f := excelize.NewFile()
	defer func() {
		_ = f.Close()
	}()
	for rowIdx, row := range rows {
		for colIdx, val := range row {
			cell, err := excelize.CoordinatesToCellName(colIdx+1, rowIdx+1)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.SetCellValue("Sheet1", cell, val); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := f.SaveAs(path); err != nil {
		t.Fatal(err)
	}
}

func columnByName(t *testing.T, p schemas.Profile, name string) schemas.ColumnProfile {
	t.Helper()
	for _, c := range p.Columns {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("column %q not found in profile columns %v", name, p.Columns)
	return schemas.ColumnProfile{}
}

func TestInspectFormats(t *testing.T) {
	dir := t.TempDir()

	tests := []struct {
		name       string
		file       string
		wantFormat string
	}{
		{
			name:       "csv",
			file:       writeFile(t, dir, "f.csv", "date,amount,ref\n2024-01-01,100.00,A1\n2024-01-02,200.00,A2\n"),
			wantFormat: "csv",
		},
		{
			name:       "json_array",
			file:       writeFile(t, dir, "f.json", `[{"date":"2024-01-01","amount":"100.00","ref":"A1"},{"date":"2024-01-02","amount":"200.00","ref":"A2"}]`),
			wantFormat: "json",
		},
		{
			name:       "ndjson",
			file:       writeFile(t, dir, "f.ndjson", "{\"date\":\"2024-01-01\",\"amount\":\"100.00\",\"ref\":\"A1\"}\n{\"date\":\"2024-01-02\",\"amount\":\"200.00\",\"ref\":\"A2\"}\n"),
			wantFormat: "json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := Inspect(context.Background(), tt.file, config.ParserCfg{}, Options{SampleValues: DefaultSampleValues})
			if err != nil {
				t.Fatalf("Inspect: %v", err)
			}
			if p.Format != tt.wantFormat {
				t.Fatalf("format = %q, want %q", p.Format, tt.wantFormat)
			}
			if p.Schema != schemas.ProfileSchemaV1 {
				t.Fatalf("schema = %q, want %q", p.Schema, schemas.ProfileSchemaV1)
			}
			if len(p.Columns) != 3 {
				t.Fatalf("columns = %d, want 3", len(p.Columns))
			}
			date := columnByName(t, p, "date")
			if date.InferredType != typeDate {
				t.Fatalf("date inferred_type = %q, want %q", date.InferredType, typeDate)
			}
			if date.DateLayout != "2006-01-02" {
				t.Fatalf("date_layout = %q, want 2006-01-02", date.DateLayout)
			}
			amount := columnByName(t, p, "amount")
			if amount.InferredType != typeAmount {
				t.Fatalf("amount inferred_type = %q, want %q", amount.InferredType, typeAmount)
			}
		})
	}
}

func TestInspectXLSXAndXLSM(t *testing.T) {
	dir := t.TempDir()
	rows := [][]string{
		{"date", "amount", "ref"},
		{"2024-01-01", "100.00", "A1"},
		{"2024-01-02", "200.00", "A2"},
	}

	for _, ext := range []string{".xlsx", ".xlsm"} {
		t.Run(ext, func(t *testing.T) {
			path := filepath.Join(dir, "f"+ext)
			writeWorkbook(t, path, rows)
			p, err := Inspect(context.Background(), path, config.ParserCfg{}, Options{SampleValues: 3})
			if err != nil {
				t.Fatalf("Inspect: %v", err)
			}
			if p.Format != "xlsx" {
				t.Fatalf("format = %q, want xlsx", p.Format)
			}
			if len(p.Columns) != 3 {
				t.Fatalf("columns = %d, want 3", len(p.Columns))
			}
		})
	}
}

func TestInspectEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "empty.csv", "")
	if _, err := Inspect(context.Background(), path, config.ParserCfg{}, Options{}); err == nil {
		t.Fatal("expected error for empty file, got nil")
	}
}

func TestInspectMalformedFile(t *testing.T) {
	dir := t.TempDir()

	t.Run("bad_json", func(t *testing.T) {
		path := writeFile(t, dir, "bad.json", `{"date": "2024-01-01",`)
		if _, err := Inspect(context.Background(), path, config.ParserCfg{}, Options{}); err == nil {
			t.Fatal("expected error for malformed JSON, got nil")
		}
	})

	t.Run("ragged_csv", func(t *testing.T) {
		path := writeFile(t, dir, "ragged.csv", "a,b\n1,2,3\n")
		if _, err := Inspect(context.Background(), path, config.ParserCfg{}, Options{}); err == nil {
			t.Fatal("expected error for ragged CSV row, got nil")
		}
	})
}

func TestInspectAmbiguousColumn(t *testing.T) {
	dir := t.TempDir()
	// Pure digit strings parse equally well as "integer" and "amount".
	path := writeFile(t, dir, "f.csv", "id\n100\n200\n300\n")
	p, err := Inspect(context.Background(), path, config.ParserCfg{}, Options{})
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	id := columnByName(t, p, "id")
	if !id.Ambiguous {
		t.Fatalf("expected id column to be flagged ambiguous, candidates=%v", id.Candidates)
	}
}

func TestInspectAmountFormats(t *testing.T) {
	dir := t.TempDir()

	tests := []struct {
		name    string
		content string
	}{
		{"comma_decimal", "amount\n\"1.234,56\"\n\"2.345,67\"\n\"3.456,78\"\n"},
		{"parens_negative", "amount\n(100.00)\n200.00\n300.00\n"},
		{"currency_symbol", "amount\n$100.00\n$200.00\n$300.00\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeFile(t, dir, tt.name+".csv", tt.content)
			p, err := Inspect(context.Background(), path, config.ParserCfg{}, Options{})
			if err != nil {
				t.Fatalf("Inspect: %v", err)
			}
			amount := columnByName(t, p, "amount")
			if amount.InferredType != typeAmount {
				t.Fatalf("inferred_type = %q, want amount (candidates=%v)", amount.InferredType, amount.Candidates)
			}
			if amount.AmountFormat == nil {
				t.Fatal("expected amount_format to be set")
			}
		})
	}
}

func TestInspectBoundedAndFullScan(t *testing.T) {
	dir := t.TempDir()
	var sb strings.Builder
	sb.WriteString("id,date,amount\n")
	const total = 1500
	for i := 0; i < total; i++ {
		fmt.Fprintf(&sb, "%d,2024-01-%02d,%d.00\n", i, (i%28)+1, i)
	}
	path := writeFile(t, dir, "big.csv", sb.String())

	bounded, err := Inspect(context.Background(), path, config.ParserCfg{}, Options{})
	if err != nil {
		t.Fatalf("Inspect (bounded): %v", err)
	}
	if bounded.Scan.Full {
		t.Fatal("expected Scan.Full = false")
	}
	if bounded.Scan.RowsScanned != DefaultScanLimit {
		t.Fatalf("rows_scanned = %d, want %d", bounded.Scan.RowsScanned, DefaultScanLimit)
	}
	if !bounded.Scan.Truncated {
		t.Fatal("expected truncated = true for a file larger than the scan limit")
	}

	full, err := Inspect(context.Background(), path, config.ParserCfg{}, Options{Full: true})
	if err != nil {
		t.Fatalf("Inspect (full): %v", err)
	}
	if !full.Scan.Full {
		t.Fatal("expected Scan.Full = true")
	}
	if full.Scan.RowsScanned != total {
		t.Fatalf("rows_scanned = %d, want %d", full.Scan.RowsScanned, total)
	}
	if full.Scan.Truncated {
		t.Fatal("expected truncated = false for a full scan")
	}

	// The incremental-counter refactor (finding 3) must not change
	// classification output: uniform data yields the same match ratios
	// whether 1,000 or 1,500 rows are scanned.
	for _, name := range []string{"id", "date", "amount"} {
		b := columnByName(t, bounded, name)
		f := columnByName(t, full, name)
		if b.InferredType != f.InferredType {
			t.Fatalf("column %q: bounded inferred_type = %q, full = %q", name, b.InferredType, f.InferredType)
		}
		if b.Ambiguous != f.Ambiguous {
			t.Fatalf("column %q: bounded ambiguous = %v, full = %v", name, b.Ambiguous, f.Ambiguous)
		}
		if b.DateLayout != f.DateLayout {
			t.Fatalf("column %q: bounded date_layout = %q, full = %q", name, b.DateLayout, f.DateLayout)
		}
		if len(b.Candidates) != len(f.Candidates) {
			t.Fatalf("column %q: bounded candidates = %v, full = %v", name, b.Candidates, f.Candidates)
		}
		for i := range b.Candidates {
			if b.Candidates[i] != f.Candidates[i] {
				t.Fatalf("column %q: bounded candidates[%d] = %v, full = %v", name, i, b.Candidates[i], f.Candidates[i])
			}
		}
	}
}

func TestInspectSampleValuesDisabled(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "f.csv", "ref\nA1\nA2\nA3\n")

	p, err := Inspect(context.Background(), path, config.ParserCfg{}, Options{SampleValues: 0})
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	ref := columnByName(t, p, "ref")
	if len(ref.SampleValues) != 0 {
		t.Fatalf("expected no sample values when SampleValues=0, got %v", ref.SampleValues)
	}
}

func TestInspectSampleValuesDistinct(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "f.csv", "ref\nA1\nA1\nA2\nA1\nA3\nA4\n")

	p, err := Inspect(context.Background(), path, config.ParserCfg{}, Options{SampleValues: 3})
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	ref := columnByName(t, p, "ref")
	if len(ref.SampleValues) != 3 {
		t.Fatalf("sample_values = %v, want 3 distinct entries", ref.SampleValues)
	}
	want := []string{"A1", "A2", "A3"}
	for i, v := range want {
		if ref.SampleValues[i] != v {
			t.Fatalf("sample_values[%d] = %q, want %q", i, ref.SampleValues[i], v)
		}
	}
}

func TestInspectJSONColumnOrderIsDeterministic(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "f.ndjson", `{"date":"1","amount":"2","ref":"3","memo":"4","cur":"5","fee":"6"}`+"\n")

	want := []string{"amount", "cur", "date", "fee", "memo", "ref"}
	for run := 0; run < 20; run++ {
		p, err := Inspect(context.Background(), path, config.ParserCfg{}, Options{})
		if err != nil {
			t.Fatalf("Inspect: %v", err)
		}
		if len(p.Columns) != len(want) {
			t.Fatalf("columns = %d, want %d", len(p.Columns), len(want))
		}
		for i, col := range p.Columns {
			if col.Name != want[i] {
				t.Fatalf("run %d: columns[%d] = %q, want %q (order not deterministic)", run, i, col.Name, want[i])
			}
		}
	}
}
