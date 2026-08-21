package inference

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

func inferenceCSV(t *testing.T, headers string, refHeader string) string {
	t.Helper()
	var data strings.Builder
	data.WriteString(headers + "\n")
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&data, "2024-01-%02d,%d.00,REF-%03d\n", (i%28)+1, i+1, i)
	}
	path := filepath.Join(t.TempDir(), "input.csv")
	if err := os.WriteFile(path, []byte(data.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestInferReadyProposal(t *testing.T) {
	left := inferenceCSV(t, "date,amount,reference", "reference")
	right := inferenceCSV(t, "date,amount,reference", "reference")
	got, err := Infer(context.Background(), left, right)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "ready" || len(got.Sources) != 2 {
		t.Fatalf("unexpected proposal: %+v", got)
	}
	if !strings.Contains(got.ProposedYAML, "date_window: 1d") || !strings.Contains(got.ProposedYAML, "left_to_right:") {
		t.Fatalf("missing defaults:\n%s", got.ProposedYAML)
	}
	for _, source := range got.Sources {
		for _, mapping := range source.Mappings {
			if !mapping.Ready {
				t.Fatalf("%s %s unexpectedly unready: %+v", source.Name, mapping.Role, mapping)
			}
		}
	}
}

func TestInferNeedsInputForUnknownReference(t *testing.T) {
	left := inferenceCSV(t, "date,amount,external_key", "external_key")
	right := inferenceCSV(t, "date,amount,external_key", "external_key")
	got, err := Infer(context.Background(), left, right)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "needs_input" {
		t.Fatalf("status = %q, want needs_input", got.Status)
	}
}

func TestInferNeedsInputWhenSampleContainsParseFailures(t *testing.T) {
	path := inferenceCSV(t, "date,amount,reference", "reference")
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0) // #nosec G304 -- test path is created above.
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("invalid-date,101.00,REF-101\n"); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := Infer(context.Background(), path, path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "needs_input" {
		t.Fatalf("status = %q, want needs_input", got.Status)
	}
	if got.Sources[0].Validation.ParseErrorCount != 1 {
		t.Fatalf("parse_error_count = %d, want 1", got.Sources[0].Validation.ParseErrorCount)
	}
}

func TestInferSupportsJSONAndXLSX(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "input.json")
	var jsonRows strings.Builder
	jsonRows.WriteByte('[')
	for i := 0; i < 100; i++ {
		if i > 0 {
			jsonRows.WriteByte(',')
		}
		fmt.Fprintf(&jsonRows, `{"date":"2024-01-%02d","amount":"%d.00","transaction_id":"REF-%03d"}`, i%28+1, i+1, i)
	}
	jsonRows.WriteByte(']')
	if err := os.WriteFile(jsonPath, []byte(jsonRows.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	if proposal, err := Infer(context.Background(), jsonPath, jsonPath); err != nil || proposal.Status != "ready" {
		t.Fatalf("JSON inference = %+v, %v", proposal, err)
	}

	xlsxPath := filepath.Join(dir, "input.xlsx")
	book := excelize.NewFile()
	defer func() { _ = book.Close() }()
	for i, header := range []string{"date", "amount", "reference"} {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		if err := book.SetCellValue("Sheet1", cell, header); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 100; i++ {
		values := []string{fmt.Sprintf("2024-01-%02d", i%28+1), fmt.Sprintf("%d.00", i+1), fmt.Sprintf("REF-%03d", i)}
		for j, value := range values {
			cell, _ := excelize.CoordinatesToCellName(j+1, i+2)
			if err := book.SetCellValue("Sheet1", cell, value); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := book.SaveAs(xlsxPath); err != nil {
		t.Fatal(err)
	}
	if proposal, err := Infer(context.Background(), xlsxPath, xlsxPath); err != nil || proposal.Status != "ready" {
		t.Fatalf("XLSX inference = %+v, %v", proposal, err)
	}
}
