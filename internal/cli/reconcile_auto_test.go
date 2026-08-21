package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReconcileAutoEmitsReproducibleInferenceMetadata(t *testing.T) {
	left, right := writeInferInput(t, "reference"), writeInferInput(t, "reference")
	dir := t.TempDir()
	autoOutput := filepath.Join(dir, "auto.json")
	configPath := filepath.Join(dir, "inferred.yaml")
	explicitOutput := filepath.Join(dir, "explicit.json")
	fixedTimestamp := "2026-01-02T03:04:05Z"

	autoRoot := newRootCmd("test", "test")
	autoRoot.SetArgs([]string{"reconcile", left, right, "--auto", "--out", autoOutput, "--audit-fixed-timestamp", fixedTimestamp})
	if err := autoRoot.Execute(); err != nil {
		t.Fatalf("auto reconcile: %v", err)
	}

	autoData, err := os.ReadFile(autoOutput) // #nosec G304 -- test path is created above.
	if err != nil {
		t.Fatal(err)
	}
	var autoResult map[string]any
	if err := json.Unmarshal(autoData, &autoResult); err != nil {
		t.Fatalf("decode auto result: %v", err)
	}
	runInfo, ok := autoResult["run_info"].(map[string]any)
	if !ok {
		t.Fatalf("auto result has no run_info: %s", autoData)
	}
	inferredYAML, ok := runInfo["inferred_config"].(string)
	if !ok || inferredYAML == "" {
		t.Fatalf("auto result has no inferred_config: %#v", runInfo)
	}
	confidence, ok := runInfo["inference_confidence"].([]any)
	if !ok || len(confidence) != 2 {
		t.Fatalf("inference_confidence = %#v, want two sources", runInfo["inference_confidence"])
	}
	if err := os.WriteFile(configPath, []byte(inferredYAML), 0o600); err != nil {
		t.Fatal(err)
	}

	explicitRoot := newRootCmd("test", "test")
	explicitRoot.SetArgs([]string{
		"reconcile", "--config", configPath, "--pair", "left_to_right",
		"--out", explicitOutput, "--audit", "--audit-fixed-timestamp", fixedTimestamp,
	})
	if err := explicitRoot.Execute(); err != nil {
		t.Fatalf("explicit reconcile: %v", err)
	}
	explicitData, err := os.ReadFile(explicitOutput) // #nosec G304 -- test path is created above.
	if err != nil {
		t.Fatal(err)
	}
	var explicitResult map[string]any
	if err := json.Unmarshal(explicitData, &explicitResult); err != nil {
		t.Fatalf("decode explicit result: %v", err)
	}
	if !autoJSONEqual(autoResult["summary"], explicitResult["summary"]) || !autoJSONEqual(autoResult["matched"], explicitResult["matched"]) {
		t.Fatalf("auto result does not match explicit result")
	}
}

func autoJSONEqual(left, right any) bool {
	leftData, leftErr := json.Marshal(left)
	rightData, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftData) == string(rightData)
}

func TestReconcileAutoDoesNotWriteWhenInferenceIsAmbiguous(t *testing.T) {
	left := writeInferInput(t, "external_key")
	right := writeInferInput(t, "external_key")
	output := filepath.Join(t.TempDir(), "result.json")
	root := newRootCmd("test", "test")
	root.SetArgs([]string{"reconcile", left, right, "--auto", "--out", output})

	err := root.Execute()
	if err == nil || ExitCode(err) != ErrCodeConfig {
		t.Fatalf("expected config error, got %v", err)
	}
	var cliErr *Error
	if !errors.As(err, &cliErr) {
		t.Fatalf("error type = %T, want *Error", err)
	}
	if cliErr.Diagnostic.Code != diagnosticCodeInferenceAmbiguous {
		t.Fatalf("diagnostic code = %q, want %q", cliErr.Diagnostic.Code, diagnosticCodeInferenceAmbiguous)
	}
	if _, statErr := os.Stat(output); !os.IsNotExist(statErr) {
		t.Fatalf("ambiguous inference wrote output: %v", statErr)
	}
}

func TestReconcileAutoRejectsConfigAndWrongArgumentCount(t *testing.T) {
	left, right := writeInferInput(t, "reference"), writeInferInput(t, "reference")
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	for name, args := range map[string][]string{
		"explicit config":    {"reconcile", left, right, "--auto", "--config", configPath},
		"missing positional": {"reconcile", "--auto"},
		"extra positional":   {"reconcile", left, right, filepath.Join(t.TempDir(), "third.csv"), "--auto"},
	} {
		t.Run(name, func(t *testing.T) {
			root := newRootCmd("test", "test")
			root.SetArgs(args)
			err := root.Execute()
			if err == nil || ExitCode(err) != ErrCodeConfig {
				t.Fatalf("expected config error, got %v", err)
			}
		})
	}
}
