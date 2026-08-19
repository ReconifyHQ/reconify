package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/reconifyhq/reconify/schemas"
)

func writeInspectFixture(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "input.csv")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write input: %v", err)
	}
	return path
}

func TestInspectCommandPrintsProfile(t *testing.T) {
	inputPath := writeInspectFixture(t, "date,amount,ref\n2024-01-01,100.00,A1\n2024-01-02,200.00,A2\n")

	root := newRootCmd("test", "test")
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"inspect", inputPath, "--format", "json"})

	if err := root.Execute(); err != nil {
		t.Fatalf("inspect: %v", err)
	}

	var got schemas.Profile
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode profile: %v", err)
	}
	if got.Schema != schemas.ProfileSchemaV1 {
		t.Fatalf("schema = %q, want %q", got.Schema, schemas.ProfileSchemaV1)
	}
	if got.Format != "csv" {
		t.Fatalf("format = %q, want csv", got.Format)
	}
	if len(got.Columns) != 3 {
		t.Fatalf("columns = %d, want 3", len(got.Columns))
	}
	if got.Scan.RowsScanned != 2 {
		t.Fatalf("rows_scanned = %d, want 2", got.Scan.RowsScanned)
	}
}

func TestInspectCommandDefaultsAndFlags(t *testing.T) {
	inputPath := writeInspectFixture(t, "ref\nA1\nA2\nA3\n")

	root := newRootCmd("test", "test")
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"inspect", inputPath, "--sample-values", "0", "--full"})

	if err := root.Execute(); err != nil {
		t.Fatalf("inspect: %v", err)
	}

	var got schemas.Profile
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode profile: %v", err)
	}
	if !got.Scan.Full {
		t.Fatal("expected scan.full = true")
	}
	if len(got.Columns[0].SampleValues) != 0 {
		t.Fatalf("expected no sample values with --sample-values 0, got %v", got.Columns[0].SampleValues)
	}
}

func TestInspectCommandMissingFile(t *testing.T) {
	root := newRootCmd("test", "test")
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"inspect", filepath.Join(t.TempDir(), "missing.csv")})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if ExitCode(err) != ErrCodeConfig {
		t.Fatalf("exit code = %d, want %d", ExitCode(err), ErrCodeConfig)
	}
}

func TestInspectCommandUnknownFormat(t *testing.T) {
	inputPath := writeInspectFixture(t, "ref\nA1\n")

	root := newRootCmd("test", "test")
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"inspect", inputPath, "--format", "table"})

	if err := root.Execute(); err == nil {
		t.Fatal("expected error for unsupported --format value")
	}
}
