package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/reconifyhq/reconify/schemas"
)

func writeInferInput(t *testing.T, refColumn string) string {
	t.Helper()
	var rows strings.Builder
	rows.WriteString("date,amount," + refColumn + "\n")
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&rows, "2024-01-%02d,%d.00,REF-%03d\n", i%28+1, i+1, i)
	}
	path := filepath.Join(t.TempDir(), "input.csv")
	if err := os.WriteFile(path, []byte(rows.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestConfigInferWritesReadyConfig(t *testing.T) {
	left, right := writeInferInput(t, "reference"), writeInferInput(t, "reference")
	outPath := filepath.Join(t.TempDir(), "inferred.yaml")
	root := newRootCmd("test", "test")
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetArgs([]string{"config", "infer", "--left", left, "--right", right, "--out", outPath})
	if err := root.Execute(); err != nil {
		t.Fatalf("infer: %v", err)
	}
	var proposal schemas.ConfigProposal
	if err := json.Unmarshal(stdout.Bytes(), &proposal); err != nil {
		t.Fatal(err)
	}
	if proposal.Status != "ready" {
		t.Fatalf("status = %q, reasons: %v", proposal.Status, proposal.Reasons)
	}
	data, err := os.ReadFile(outPath) // #nosec G304 -- test path is created above.
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != proposal.ProposedYAML {
		t.Fatal("written YAML differs from proposal")
	}
}

func TestConfigInferDoesNotWriteAmbiguousProposal(t *testing.T) {
	left, right := writeInferInput(t, "external_key"), writeInferInput(t, "external_key")
	outPath := filepath.Join(t.TempDir(), "inferred.yaml")
	root := newRootCmd("test", "test")
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetArgs([]string{"config", "infer", "--left", left, "--right", right, "--out", outPath})
	err := root.Execute()
	if err == nil || ExitCode(err) != ErrCodeConfig {
		t.Fatalf("expected config error, got %v", err)
	}
	if _, statErr := os.Stat(outPath); !os.IsNotExist(statErr) {
		t.Fatalf("ambiguous inference wrote output: %v", statErr)
	}
	var proposal schemas.ConfigProposal
	if err := json.Unmarshal(stdout.Bytes(), &proposal); err != nil {
		t.Fatal(err)
	}
	if proposal.Status != "needs_input" {
		t.Fatalf("status = %q", proposal.Status)
	}
}

func TestConfigInferMissingFileUsesUnreadableDiagnostic(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.csv")
	right := writeInferInput(t, "reference")
	root := newRootCmd("test", "test")
	root.SetArgs([]string{"config", "infer", "--left", missing, "--right", right})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected missing input error")
	}
	var cliErr *Error
	if !errors.As(err, &cliErr) {
		t.Fatalf("error type = %T, want *Error", err)
	}
	if cliErr.Diagnostic.Code != diagnosticCodeInputUnreadable {
		t.Fatalf("diagnostic code = %q, want %q", cliErr.Diagnostic.Code, diagnosticCodeInputUnreadable)
	}
}
