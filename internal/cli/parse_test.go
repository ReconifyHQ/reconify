package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeParseFixture(t *testing.T, groupCol string) (configPath, inputPath string) {
	t.Helper()
	dir := t.TempDir()
	inputPath = filepath.Join(dir, "input.csv")
	configPath = filepath.Join(dir, "reconify.yaml")

	if err := os.WriteFile(inputPath, []byte("date,amount,currency,description,txn_id,invoice_number\n2026-01-01,100,USD,salary,REF-1,INV-9\n"), 0o600); err != nil {
		t.Fatalf("write input: %v", err)
	}

	cfg := `version: 1
sources:
  bank:
    file_pattern: ` + inputPath + `
    parser:
      type: csv
      date_col: date
      date_layout: "2006-01-02"
      amount_col: amount
      multiplier: 1
      currency_col: currency
      name_col: description
      ref_col: txn_id
`
	if groupCol != "" {
		cfg += "      group_col: " + groupCol + "\n"
	}
	if err := os.WriteFile(configPath, []byte(cfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return configPath, inputPath
}

func runParse(t *testing.T, configPath, inputPath, format string) string {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	origStdout := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = origStdout }()

	root := newRootCmd("test", "test")
	root.SetArgs([]string{"parse", "--config", configPath, "--source", "bank", "--file", inputPath, "--format", format})
	root.SetErr(&bytes.Buffer{})
	execErr := root.Execute()

	if err := w.Close(); err != nil {
		t.Fatalf("close pipe: %v", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	if execErr != nil {
		t.Fatalf("parse --format %s: %v", format, execErr)
	}
	return string(out)
}

func TestParseCSVIncludesGroupKeyColumn(t *testing.T) {
	configPath, inputPath := writeParseFixture(t, "invoice_number")

	out := runParse(t, configPath, inputPath, "csv")

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected header + 1 row, got:\n%s", out)
	}
	if !strings.HasSuffix(strings.TrimSpace(lines[0]), ",group_key") {
		t.Errorf("header missing trailing group_key column: %q", lines[0])
	}
	if !strings.HasSuffix(strings.TrimSpace(lines[1]), ",INV-9") {
		t.Errorf("row missing group_key value INV-9: %q", lines[1])
	}
}

func TestParseCSVGroupKeyFallsBackToReference(t *testing.T) {
	configPath, inputPath := writeParseFixture(t, "")

	out := runParse(t, configPath, inputPath, "csv")

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected header + 1 row, got:\n%s", out)
	}
	if !strings.HasSuffix(strings.TrimSpace(lines[1]), ",REF-1") {
		t.Errorf("row group_key should fall back to reference REF-1: %q", lines[1])
	}
}

func TestParseTableIncludesGroupKeyColumn(t *testing.T) {
	configPath, inputPath := writeParseFixture(t, "invoice_number")

	out := runParse(t, configPath, inputPath, "table")

	if !strings.Contains(out, "GROUP_KEY") {
		t.Errorf("table header missing GROUP_KEY column:\n%s", out)
	}
	if !strings.Contains(out, "INV-9") {
		t.Errorf("table row missing group_key value INV-9:\n%s", out)
	}
}
