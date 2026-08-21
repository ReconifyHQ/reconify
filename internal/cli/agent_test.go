package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentProfileAppliesMachineReadableDefaults(t *testing.T) {
	root := newRootCmd("test", "test")
	root.SetArgs([]string{"--agent", "reconcile", "--pair", "missing"})
	if err := root.Execute(); err == nil {
		t.Fatal("expected missing config error")
	}

	reconcile, _, err := root.Find([]string{"reconcile"})
	if err != nil {
		t.Fatal(err)
	}
	format, err := reconcile.Flags().GetString("format")
	if err != nil {
		t.Fatal(err)
	}
	if format != "ndjson" {
		t.Fatalf("agent format = %q, want ndjson", format)
	}
	resultMode, err := reconcile.Flags().GetString("result-mode")
	if err != nil {
		t.Fatal(err)
	}
	if resultMode != "" {
		t.Fatalf("agent result mode flag = %q, want empty built-in default", resultMode)
	}
	failIfExceptions, err := reconcile.Flags().GetBool("fail-if-exceptions")
	if err != nil {
		t.Fatal(err)
	}
	if failIfExceptions {
		t.Fatal("--agent must not imply --fail-if-exceptions")
	}
	if ErrorFormat() != "json" {
		t.Fatalf("agent error format = %q, want json", ErrorFormat())
	}
}

func TestAgentProfilePreservesExplicitOverrides(t *testing.T) {
	root := newRootCmd("test", "test")
	root.SetArgs([]string{
		"--agent", "--error-format", "text",
		"reconcile", "--pair", "missing", "--format", "json", "--result-mode", "all",
	})
	if err := root.Execute(); err == nil {
		t.Fatal("expected missing config error")
	}

	reconcile, _, err := root.Find([]string{"reconcile"})
	if err != nil {
		t.Fatal(err)
	}
	format, _ := reconcile.Flags().GetString("format")
	resultMode, _ := reconcile.Flags().GetString("result-mode")
	if format != "json" || resultMode != "all" {
		t.Fatalf("explicit overrides changed: format=%q result-mode=%q", format, resultMode)
	}
	if ErrorFormat() != "text" {
		t.Fatalf("explicit error format changed to %q", ErrorFormat())
	}
}

func TestAgentProfilePreservesPairResultMode(t *testing.T) {
	dir := t.TempDir()
	leftPath := filepath.Join(dir, "left.csv")
	rightPath := filepath.Join(dir, "right.csv")
	configPath := filepath.Join(dir, "reconify.yaml")
	resultPath := filepath.Join(dir, "result.ndjson")
	data := []byte("date,amount,reference\n2024-01-01,1.00,ref-1\n")
	for _, path := range []string{leftPath, rightPath} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	configText := strings.Replace(buildTestConfig(leftPath, rightPath),
		"    date_window: 0d", "    date_window: 0d\n    result_mode: summary_only", 1)
	if err := os.WriteFile(configPath, []byte(configText), 0o600); err != nil {
		t.Fatal(err)
	}

	bin := runReconcileBinary(t)
	// #nosec G204 -- test invokes the locally built CLI binary with t.TempDir paths.
	command := exec.Command(bin, "--agent", "reconcile", "--config", configPath, "--pair", "pair", "--out", resultPath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("agent reconcile failed: %v\n%s", err, output)
	}
	result, err := os.ReadFile(resultPath) // #nosec G304 -- t.TempDir test file.
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(result), `"type":"summary"`) ||
		!strings.Contains(string(result), `"result_mode":"summary_only"`) {
		t.Fatalf("agent profile did not preserve pair result_mode: %s", result)
	}
	if strings.Contains(string(result), `"type":"match"`) {
		t.Fatalf("agent profile overrode pair summary_only mode: %s", result)
	}
}

func TestAgentProfileRejectsInteractiveCommands(t *testing.T) {
	root := newRootCmd("test", "test")
	root.SetArgs([]string{"--agent", "config", "init"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected interactive command rejection")
	}
	if ExitCode(err) != ErrCodeConfig || LegacyErrorCode(err) != "config_error" {
		t.Fatalf("compatibility error = (%d, %q)", ExitCode(err), LegacyErrorCode(err))
	}
	envelope := DiagnosticEnvelope(err)
	if envelope.Diagnostic.Code != diagnosticCodeInteractiveUnsupported {
		t.Fatalf("diagnostic code = %q", envelope.Diagnostic.Code)
	}
	if !strings.Contains(envelope.Diagnostic.Message, "config infer") ||
		len(envelope.Diagnostic.Suggestions) != 1 ||
		!strings.Contains(envelope.Diagnostic.Suggestions[0], "config infer") {
		t.Fatalf("diagnostic does not name a non-interactive alternative: %+v", envelope.Diagnostic)
	}
	if ErrorFormat() != "json" {
		t.Fatalf("agent error format = %q, want json", ErrorFormat())
	}
}

func TestNormalProfileDefaultsRemainUnchanged(t *testing.T) {
	root := newRootCmd("test", "test")
	root.SetArgs([]string{"reconcile", "--pair", "missing"})
	if err := root.Execute(); err == nil {
		t.Fatal("expected missing config error")
	}

	reconcile, _, err := root.Find([]string{"reconcile"})
	if err != nil {
		t.Fatal(err)
	}
	format, _ := reconcile.Flags().GetString("format")
	resultMode, _ := reconcile.Flags().GetString("result-mode")
	if format != "json" || resultMode != "" {
		t.Fatalf("normal defaults changed: format=%q result-mode=%q", format, resultMode)
	}
	if ErrorFormat() != "text" {
		t.Fatalf("normal error format = %q, want text", ErrorFormat())
	}
}
