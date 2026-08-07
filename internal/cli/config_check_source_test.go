package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigCheckSourcePrintsAvailableColumnsOnMismatch(t *testing.T) {
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "input.csv")
	configPath := filepath.Join(dir, "reconify.yaml")

	if err := os.WriteFile(inputPath, []byte("transaction_date,transaction_amount,currency,description,ref_id\n2026-01-01,100,USD,salary,REF-1\n"), 0o600); err != nil {
		t.Fatalf("write input: %v", err)
	}

	config := `version: 1
sources:
  bank:
    file_pattern: ` + inputPath + `
    parser:
      type: csv
      date_col: Date
      date_layout: "2006-01-02"
      amount_col: Amount
      multiplier: 1
`
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	root := newRootCmd("test", "test")
	root.SetArgs([]string{"config", "check-source", "--config", configPath, "--source", "bank", "--file", inputPath})

	var stderr bytes.Buffer
	root.SetErr(&stderr)

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for missing columns")
	}

	out := stderr.String()
	if !strings.Contains(out, "Available columns:") {
		t.Fatalf("stderr missing 'Available columns:', got:\n%s", out)
	}
	if !strings.Contains(out, "transaction_date") {
		t.Fatalf("stderr missing actual header 'transaction_date', got:\n%s", out)
	}
}
