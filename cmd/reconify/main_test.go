package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/reconifyhq/reconify/schemas"
)

func buildTestBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "reconify")
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/reconify")
	cmd.Dir = root
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, output)
	}
	return bin
}

func runTestBinary(t *testing.T, bin string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err := cmd.Run()
	if err == nil {
		return out.String(), errOut.String(), 0
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("run %v: %v", args, err)
	}
	return out.String(), errOut.String(), exitErr.ExitCode()
}

func TestJSONDiagnosticEnvelopeIsAdditiveAndKeepsStreamsSeparate(t *testing.T) {
	bin := buildTestBinary(t)
	missingConfig := filepath.Join(t.TempDir(), "missing.yaml")
	stdout, stderr, exitCode := runTestBinary(t, bin, "--error-format", "json", "config", "validate", "--config", missingConfig)
	if exitCode != 2 {
		t.Fatalf("exit code = %d, want 2; stderr=%s", exitCode, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	var envelope schemas.DiagnosticEnvelope
	if err := json.Unmarshal([]byte(strings.TrimSpace(stderr)), &envelope); err != nil {
		t.Fatalf("decode stderr JSON: %v\nstderr=%s", err, stderr)
	}
	if envelope.Error == "" || envelope.Code != "config_error" || envelope.OK || envelope.Schema != schemas.DiagnosticSchemaV1 {
		t.Fatalf("envelope = %+v", envelope)
	}
	if envelope.Diagnostic.Code != "CONFIG_INVALID" || envelope.Diagnostic.Category != "config" {
		t.Fatalf("diagnostic = %+v", envelope.Diagnostic)
	}
}

func TestDefaultTextDiagnosticRemainsLegacyPresentation(t *testing.T) {
	bin := buildTestBinary(t)
	missingConfig := filepath.Join(t.TempDir(), "missing.yaml")
	stdout, stderr, exitCode := runTestBinary(t, bin, "config", "validate", "--config", missingConfig)
	if exitCode != 2 {
		t.Fatalf("exit code = %d, want 2; stderr=%s", exitCode, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.HasPrefix(stderr, "Error: failed to load config:") || strings.Contains(stderr, "diagnostic") {
		t.Fatalf("text stderr changed unexpectedly: %q", stderr)
	}
}

func TestJSONDiagnosticCoversGenericCobraErrors(t *testing.T) {
	bin := buildTestBinary(t)
	stdout, stderr, exitCode := runTestBinary(t, bin, "--error-format", "json", "reconcile", "--bogus")
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1; stderr=%s", exitCode, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	var envelope schemas.DiagnosticEnvelope
	if err := json.Unmarshal([]byte(strings.TrimSpace(stderr)), &envelope); err != nil {
		t.Fatalf("decode stderr JSON: %v\nstderr=%s", err, stderr)
	}
	if envelope.Code != "error" || envelope.Diagnostic.Code != "INTERNAL_ERROR" || envelope.Diagnostic.Category != "internal" {
		t.Fatalf("envelope = %+v", envelope)
	}
}
