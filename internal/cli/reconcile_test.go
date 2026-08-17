package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/reconifyhq/reconify/engine"
)

func TestTelemetryOutputIsNDJSONAndSeparateFromResults(t *testing.T) {
	dir := t.TempDir()
	progressPath := filepath.Join(dir, "progress.ndjson")
	resultPath := filepath.Join(dir, "result.ndjson")
	options, closeFn, err := openTelemetry(false, progressPath, 1, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := options.Sink(engine.TelemetryEvent{Type: "progress", RunID: "run", Timestamp: time.Now(), Stage: "left_match", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(resultPath, []byte(`{"type":"summary"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	closeFn()
	f, err := os.Open(progressPath) // #nosec G304 -- t.TempDir test file.
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var event engine.TelemetryEvent
	if err := json.NewDecoder(bufio.NewReader(f)).Decode(&event); err != nil {
		t.Fatalf("progress output is not NDJSON: %v", err)
	}
	if event.Type != "progress" {
		t.Fatalf("event type = %q", event.Type)
	}
	result, err := os.ReadFile(resultPath) // #nosec G304 -- t.TempDir test file.
	if err != nil {
		t.Fatal(err)
	}
	if string(result) != `{"type":"summary"}` {
		t.Fatalf("result output changed: %q", result)
	}
}

func TestValidateProgressOutputRejectsStdoutAndCollisions(t *testing.T) {
	if err := validateProgressOutput("-", "results.ndjson"); err == nil {
		t.Fatal("expected stdout rejection")
	}
	path := filepath.Join(t.TempDir(), "same.ndjson")
	if err := validateProgressOutput(path, path); err == nil {
		t.Fatal("expected output collision rejection")
	}
	inputPath := filepath.Join(t.TempDir(), "input.csv")
	if err := os.WriteFile(inputPath, []byte("input"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateProgressOutput(inputPath, "result.ndjson", inputPath); err == nil {
		t.Fatal("expected input collision rejection")
	}
	linkPath := filepath.Join(t.TempDir(), "progress.ndjson")
	if err := os.Symlink(inputPath, linkPath); err != nil {
		t.Skipf("symlink creation not supported: %v", err)
	}
	if err := validateProgressOutput(linkPath, "result.ndjson", inputPath); err == nil {
		t.Fatal("expected telemetry symlink rejection")
	}
}

func TestReconcileProgressOutPreservesResultOutput(t *testing.T) {
	dir := t.TempDir()
	leftPath := filepath.Join(dir, "left.csv")
	rightPath := filepath.Join(dir, "right.csv")
	configPath := filepath.Join(dir, "reconify.yaml")
	resultPath := filepath.Join(dir, "result.ndjson")
	progressPath := filepath.Join(dir, "progress.ndjson")
	csv := "date,amount,reference\n2024-01-01,1.00,ref-1\n"
	for _, path := range []string{leftPath, rightPath} {
		if err := os.WriteFile(path, []byte(csv), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	yaml := "version: 1\nsources:\n  left:\n    file_pattern: " + leftPath + "\n    parser: &parser\n      type: csv\n      date_col: date\n      date_layout: \"2006-01-02\"\n      amount_col: amount\n      multiplier: 100\n      ref_col: reference\n  right:\n    file_pattern: " + rightPath + "\n    parser: *parser\npairs:\n  pair:\n    left: left\n    right: right\n    date_window: 0d\n"
	if err := os.WriteFile(configPath, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	// #nosec G204 -- test invokes the local Go toolchain with a fixed program and t.TempDir paths.
	command := exec.Command("go", "run", "./cmd/reconify", "reconcile", "--config", configPath, "--pair", "pair", "--format", "ndjson", "--out", resultPath, "--progress-out", progressPath, "--progress-every", "1")
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("reconcile with progress output failed: %v\n%s", err, output)
	}
	result, err := os.ReadFile(resultPath) // #nosec G304 -- t.TempDir test file.
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(result), `"type":"summary"`) {
		t.Fatalf("result output does not contain a reconciliation summary: %s", result)
	}
	progress, err := os.ReadFile(progressPath) // #nosec G304 -- t.TempDir test file.
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(progress), `"type":"progress"`) || !strings.Contains(string(progress), `"stage":"right_index"`) {
		t.Fatalf("progress output missing lifecycle NDJSON: %s", progress)
	}
}

func TestReconcilePartitionedMultiSourceCLI(t *testing.T) {
	dir := t.TempDir()
	leftPath := filepath.Join(dir, "left.csv")
	firstPath := filepath.Join(dir, "first.csv")
	secondPath := filepath.Join(dir, "second.csv")
	configPath := filepath.Join(dir, "reconify.yaml")
	resultPath := filepath.Join(dir, "result.json")
	spillPath := filepath.Join(dir, "spill")
	csv := func(row string) []byte {
		return []byte("date,amount,reference\n" + row + "\n")
	}
	if err := os.WriteFile(leftPath, []byte("date,amount,reference\n2024-01-01,100,REF-1\n2024-01-01,200,REF-2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(firstPath, csv("2024-01-01,100,REF-1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secondPath, csv("2024-01-01,200,REF-2"), 0o600); err != nil {
		t.Fatal(err)
	}
	yaml := "version: 1\nindex:\n  backend: partitioned\n  partition_count: 2\n  spill_dir: " + spillPath + "\nsources:\n  left:\n    file_pattern: " + leftPath + "\n    parser: &parser\n      type: csv\n      date_col: date\n      date_layout: \"2006-01-02\"\n      amount_col: amount\n      multiplier: 1\n      ref_col: reference\n  first:\n    file_pattern: " + firstPath + "\n    parser: *parser\n  second:\n    file_pattern: " + secondPath + "\n    parser: *parser\npairs:\n  pair:\n    left: left\n    rights: [first, second]\n    date_window: 0d\n"
	if err := os.WriteFile(configPath, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	// #nosec G204 -- test invokes the local Go toolchain with fixed program arguments and t.TempDir paths.
	command := exec.Command("go", "run", "./cmd/reconify", "reconcile", "--config", configPath, "--pair", "pair", "--format", "json", "--partition-workers", "2", "--partition-queue-capacity", "1", "--out", resultPath)
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("partitioned multi-source reconcile failed: %v\n%s", err, output)
	}
	result, err := os.ReadFile(resultPath) // #nosec G304 -- t.TempDir test file.
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(result), `"matched": 2`) || !strings.Contains(string(result), `"by_source"`) {
		t.Fatalf("partitioned multi-source result is missing expected summaries: %s", result)
	}
	entries, err := os.ReadDir(spillPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("partitioned spill artifacts remain: %v", entries)
	}
}

func TestReconcileRejectsInvalidTelemetryIntervals(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"reconcile", "--pair", "pair", "--progress-every", "0"},
		{"reconcile", "--pair", "pair", "--heartbeat-every", "0s"},
	} {
		// #nosec G204 -- test invokes the local Go toolchain with fixed argument cases below.
		command := exec.Command("go", append([]string{"run", "./cmd/reconify"}, args...)...)
		command.Dir = root
		output, err := command.CombinedOutput()
		if err == nil {
			t.Fatalf("expected invalid telemetry interval to fail: %v", args)
		}
		if !strings.Contains(string(output), "must be") {
			t.Fatalf("invalid telemetry interval error was unclear: %s", output)
		}
	}
}

func TestResolveFile_RejectsGlobOutsideConfigDir(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	outsideDir := filepath.Join(root, "outside")
	if err := os.Mkdir(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outsideDir, 0700); err != nil {
		t.Fatal(err)
	}
	outsideFile := filepath.Join(outsideDir, "bank.csv")
	if err := os.WriteFile(outsideFile, []byte("date,amount\n"), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := resolveFile("", "../outside/*.csv", configDir)
	if err == nil {
		t.Fatal("expected error for glob that resolves outside config directory")
	}
}

func TestResolveFile_AllowsGlobInsideConfigDir(t *testing.T) {
	configDir := t.TempDir()
	dataDir := filepath.Join(configDir, "data")
	if err := os.Mkdir(dataDir, 0700); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dataDir, "bank.csv")
	if err := os.WriteFile(want, []byte("date,amount\n"), 0600); err != nil {
		t.Fatal(err)
	}

	got, err := resolveFile("", "data/*.csv", configDir)
	if err != nil {
		t.Fatalf("resolveFile returned error: %v", err)
	}
	if got != want {
		t.Fatalf("resolveFile = %q, want %q", got, want)
	}
}

func TestResolveFile_AllowsAbsoluteGlobOutsideConfigDir(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	outsideDir := filepath.Join(root, "outside")
	if err := os.Mkdir(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outsideDir, 0700); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(outsideDir, "bank.csv")
	if err := os.WriteFile(want, []byte("date,amount\n"), 0600); err != nil {
		t.Fatal(err)
	}

	got, err := resolveFile("", filepath.Join(outsideDir, "*.csv"), configDir)
	if err != nil {
		t.Fatalf("resolveFile returned error: %v", err)
	}
	if got != want {
		t.Fatalf("resolveFile = %q, want %q", got, want)
	}
}

func TestResolveFile_RejectsSymlinkThatEscapesConfigDir(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	outsideDir := filepath.Join(root, "outside")
	if err := os.Mkdir(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outsideDir, 0700); err != nil {
		t.Fatal(err)
	}
	outsideFile := filepath.Join(outsideDir, "bank.csv")
	if err := os.WriteFile(outsideFile, []byte("date,amount\n"), 0600); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(configDir, "bank.csv")
	if err := os.Symlink(outsideFile, linkPath); err != nil {
		t.Fatal(err)
	}

	_, err := resolveFile("", "*.csv", configDir)
	if err == nil {
		t.Fatal("expected error for symlinked match that resolves outside config directory")
	}
}

func TestReconcileOutputDoesNotPublishBeforeCommit(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "results.json")
	output, err := openReconcileOutput(target, false)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Cleanup()

	if _, err := output.File.WriteString(`{"ok":true}`); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("target existed before commit; err=%v", err)
	}
}

func TestReconcileOutputCommitWritesFinalFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "results.json")
	output, err := openReconcileOutput(target, false)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Cleanup()

	if _, err := output.File.WriteString(`{"ok":true}`); err != nil {
		t.Fatal(err)
	}
	if err := output.Commit(); err != nil {
		t.Fatalf("Commit returned error: %v", err)
	}
	got, err := os.ReadFile(target) // #nosec G304 -- target is inside t.TempDir() for this test.
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"ok":true}` {
		t.Fatalf("target contents = %q", got)
	}
}

func TestReconcileOutputCommitRefusesSymlinkCreatedAfterOpen(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "results.json")
	victim := filepath.Join(dir, "victim.txt")
	if err := os.WriteFile(victim, []byte("victim"), 0600); err != nil {
		t.Fatal(err)
	}
	output, err := openReconcileOutput(target, false)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Cleanup()

	if _, err := output.File.WriteString(`{"ok":true}`); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, target); err != nil {
		t.Skipf("symlink creation not supported: %v", err)
	}
	if err := output.Commit(); err == nil {
		t.Fatal("expected Commit to refuse symlink output path")
	}
	got, err := os.ReadFile(victim) // #nosec G304 -- victim is inside t.TempDir() for this test.
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "victim" {
		t.Fatalf("victim was modified: %q", got)
	}
}

// buildTestConfig returns a minimal YAML config string that can be written to disk for
// --result-mode integration tests.
func buildTestConfig(leftPath, rightPath string) string {
	return "version: 1\nsources:\n  left:\n    file_pattern: " + leftPath +
		"\n    parser: &parser\n      type: csv\n      date_col: date\n      date_layout: \"2006-01-02\"\n" +
		"      amount_col: amount\n      multiplier: 100\n      ref_col: reference\n" +
		"  right:\n    file_pattern: " + rightPath + "\n    parser: *parser\n" +
		"pairs:\n  pair:\n    left: left\n    right: right\n    date_window: 0d\n"
}

func TestReconcileResultMode_ExceptionsOnly_SuppressesMatches(t *testing.T) {
	dir := t.TempDir()
	leftPath := filepath.Join(dir, "left.csv")
	rightPath := filepath.Join(dir, "right.csv")
	configPath := filepath.Join(dir, "reconify.yaml")
	resultPath := filepath.Join(dir, "result.ndjson")

	leftCSV := "date,amount,reference\n2024-01-01,1.00,ref-match\n2024-01-01,2.00,ref-only-l\n"
	rightCSV := "date,amount,reference\n2024-01-01,1.00,ref-match\n"
	if err := os.WriteFile(leftPath, []byte(leftCSV), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rightPath, []byte(rightCSV), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(buildTestConfig(leftPath, rightPath)), 0o600); err != nil {
		t.Fatal(err)
	}

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	// #nosec G204 -- test invokes the local Go toolchain with fixed arguments.
	command := exec.Command("go", "run", "./cmd/reconify", "reconcile",
		"--config", configPath, "--pair", "pair",
		"--format", "ndjson", "--out", resultPath,
		"--result-mode", "exceptions_only")
	command.Dir = root
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("reconcile failed: %v\n%s", err, out)
	}

	data, err := os.ReadFile(resultPath) // #nosec G304 -- t.TempDir test file.
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")

	hasMatch := false
	hasUnmatchedL := false
	hasSummary := false
	var summaryResultMode string

	for _, line := range lines {
		var ev map[string]interface{}
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("invalid NDJSON line: %v — %s", err, line)
		}
		// NDJSON format: {"type":"...", "data":{...}}
		switch ev["type"] {
		case "match":
			hasMatch = true
		case "unmatched_left", "unmatched_right":
			hasUnmatchedL = true
		case "summary":
			hasSummary = true
			if d, ok := ev["data"].(map[string]interface{}); ok {
				summaryResultMode, _ = d["result_mode"].(string)
			}
		}
	}

	if hasMatch {
		t.Error("exceptions_only: clean match event should be suppressed")
	}
	if !hasUnmatchedL {
		t.Error("exceptions_only: unmatched event should be present")
	}
	if !hasSummary {
		t.Error("exceptions_only: summary should still be written")
	}
	if summaryResultMode != "exceptions_only" {
		t.Errorf("summary result_mode = %q, want %q", summaryResultMode, "exceptions_only")
	}
}

func TestReconcileResultMode_SummaryOnly_NoItemEvents(t *testing.T) {
	dir := t.TempDir()
	leftPath := filepath.Join(dir, "left.csv")
	rightPath := filepath.Join(dir, "right.csv")
	configPath := filepath.Join(dir, "reconify.yaml")
	resultPath := filepath.Join(dir, "result.ndjson")

	csv := "date,amount,reference\n2024-01-01,1.00,ref-1\n"
	for _, path := range []string{leftPath, rightPath} {
		if err := os.WriteFile(path, []byte(csv), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(configPath, []byte(buildTestConfig(leftPath, rightPath)), 0o600); err != nil {
		t.Fatal(err)
	}

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	// #nosec G204 -- test invokes the local Go toolchain with fixed arguments.
	command := exec.Command("go", "run", "./cmd/reconify", "reconcile",
		"--config", configPath, "--pair", "pair",
		"--format", "ndjson", "--out", resultPath,
		"--result-mode", "summary_only")
	command.Dir = root
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("reconcile failed: %v\n%s", err, out)
	}

	data, err := os.ReadFile(resultPath) // #nosec G304 -- t.TempDir test file.
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")

	summaryCount := 0
	itemCount := 0
	var summaryResultMode string

	for _, line := range lines {
		if line == "" {
			continue
		}
		var ev map[string]interface{}
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("invalid NDJSON line: %v — %s", err, line)
		}
		// NDJSON format: {"type":"...", "data":{...}}
		switch ev["type"] {
		case "summary":
			summaryCount++
			if d, ok := ev["data"].(map[string]interface{}); ok {
				summaryResultMode, _ = d["result_mode"].(string)
			}
		case "index_selection", "run_info":
			// metadata lines are always permitted
		default:
			itemCount++
		}
	}

	if itemCount != 0 {
		t.Errorf("summary_only: expected no item events, got %d", itemCount)
	}
	if summaryCount != 1 {
		t.Errorf("summary_only: expected 1 summary, got %d", summaryCount)
	}
	if summaryResultMode != "summary_only" {
		t.Errorf("summary result_mode = %q, want %q", summaryResultMode, "summary_only")
	}
}

func TestReconcileResultMode_InvalidValue_Rejected(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	// #nosec G204 -- test invokes the local Go toolchain with a fixed argument.
	command := exec.Command("go", "run", "./cmd/reconify", "reconcile", "--pair", "pair", "--result-mode", "bad_value")
	command.Dir = root
	out, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("expected invalid --result-mode to fail")
	}
	if !strings.Contains(string(out), "result-mode") {
		t.Fatalf("error should mention result-mode, got: %s", out)
	}
}

func TestReconcileResultMode_CLIOverridesPairConfig(t *testing.T) {
	dir := t.TempDir()
	leftPath := filepath.Join(dir, "left.csv")
	rightPath := filepath.Join(dir, "right.csv")
	configPath := filepath.Join(dir, "reconify.yaml")
	resultPath := filepath.Join(dir, "result.ndjson")

	leftCSV := "date,amount,reference\n2024-01-01,1.00,ref-match\n"
	rightCSV := "date,amount,reference\n2024-01-01,1.00,ref-match\n"
	for _, p := range []struct{ path, data string }{{leftPath, leftCSV}, {rightPath, rightCSV}} {
		if err := os.WriteFile(p.path, []byte(p.data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Pair config says summary_only; CLI flag says exceptions_only → CLI wins.
	yamlCfg := "version: 1\nsources:\n  left:\n    file_pattern: " + leftPath +
		"\n    parser: &parser\n      type: csv\n      date_col: date\n      date_layout: \"2006-01-02\"\n" +
		"      amount_col: amount\n      multiplier: 100\n      ref_col: reference\n" +
		"  right:\n    file_pattern: " + rightPath + "\n    parser: *parser\n" +
		"pairs:\n  pair:\n    left: left\n    right: right\n    date_window: 0d\n    result_mode: summary_only\n"
	if err := os.WriteFile(configPath, []byte(yamlCfg), 0o600); err != nil {
		t.Fatal(err)
	}

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	// #nosec G204 -- test invokes the local Go toolchain with fixed arguments.
	command := exec.Command("go", "run", "./cmd/reconify", "reconcile",
		"--config", configPath, "--pair", "pair",
		"--format", "ndjson", "--out", resultPath,
		"--result-mode", "exceptions_only") // CLI override
	command.Dir = root
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("reconcile failed: %v\n%s", err, out)
	}

	data, err := os.ReadFile(resultPath) // #nosec G304 -- t.TempDir test file.
	if err != nil {
		t.Fatal(err)
	}
	var summaryResultMode string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var ev map[string]interface{}
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		// NDJSON format: {"type":"...", "data":{...}}
		if ev["type"] == "summary" {
			if d, ok := ev["data"].(map[string]interface{}); ok {
				summaryResultMode, _ = d["result_mode"].(string)
			}
		}
	}
	// CLI --result-mode=exceptions_only overrides pair result_mode=summary_only.
	if summaryResultMode != "exceptions_only" {
		t.Errorf("CLI --result-mode did not override pair config: got %q, want %q", summaryResultMode, "exceptions_only")
	}
}

// runReconcileBinary builds the reconify CLI binary once and returns its path.
// go run always exits 1 when the inner program exits non-zero, so to observe
// the real exit code (3 vs 4) we build the binary and run it directly.
func runReconcileBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "reconify")
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	// #nosec G204 -- test builds the local CLI binary with a fixed target.
	build := exec.Command("go", "build", "-o", bin, "./cmd/reconify")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, out)
	}
	return bin
}

func TestReconcileExitError(t *testing.T) {
	cases := []struct {
		name             string
		sum              engine.Summary
		failIfUnmatched  bool
		failIfExceptions bool
		wantCode         int
		wantErrCode      string
	}{
		{
			name:     "no flags clean run",
			sum:      engine.Summary{MatchedCount: 1},
			wantCode: 0,
		},
		{
			name:             "fail-if-exceptions clean run",
			sum:              engine.Summary{MatchedCount: 1},
			failIfExceptions: true,
			wantCode:         0,
		},
		{
			name:             "fail-if-exceptions amount_diff",
			sum:              engine.Summary{AmountDiffCount: 1},
			failIfExceptions: true,
			wantCode:         4,
			wantErrCode:      "exceptions",
		},
		{
			name:             "fail-if-exceptions timing_diff",
			sum:              engine.Summary{TimingDiffCount: 1},
			failIfExceptions: true,
			wantCode:         4,
			wantErrCode:      "exceptions",
		},
		{
			name:             "fail-if-exceptions unmatched superset",
			sum:              engine.Summary{UnmatchedLeft: 1},
			failIfExceptions: true,
			wantCode:         4,
			wantErrCode:      "exceptions",
		},
		{
			name:            "fail-if-unmatched unmatched only",
			sum:             engine.Summary{UnmatchedLeft: 1},
			failIfUnmatched: true,
			wantCode:        3,
			wantErrCode:     "unmatched",
		},
		{
			name:            "fail-if-unmatched amount_diff only does not exit",
			sum:             engine.Summary{AmountDiffCount: 1},
			failIfUnmatched: true,
			wantCode:        0,
		},
		{
			name:             "both flags unmatched only -> code 4 precedence",
			sum:              engine.Summary{UnmatchedLeft: 1},
			failIfUnmatched:  true,
			failIfExceptions: true,
			wantCode:         4,
			wantErrCode:      "exceptions",
		},
		{
			name:             "both flags amount_diff + unmatched -> code 4",
			sum:              engine.Summary{AmountDiffCount: 1, UnmatchedLeft: 1},
			failIfUnmatched:  true,
			failIfExceptions: true,
			wantCode:         4,
			wantErrCode:      "exceptions",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := reconcileExitError(tc.sum, tc.failIfUnmatched, tc.failIfExceptions)
			if tc.wantCode == 0 {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error with code %d, got nil", tc.wantCode)
			}
			cliErr, ok := err.(*Error)
			if !ok {
				t.Fatalf("expected *cli.Error, got %T (%v)", err, err)
			}
			if cliErr.Code != tc.wantCode {
				t.Errorf("code = %d, want %d", cliErr.Code, tc.wantCode)
			}
			if cliErr.ErrCode != tc.wantErrCode {
				t.Errorf("errcode = %q, want %q", cliErr.ErrCode, tc.wantErrCode)
			}
		})
	}
}

func TestReconcileFailIfExceptionsFlag(t *testing.T) {
	bin := runReconcileBinary(t)

	cases := []struct {
		name     string
		leftCSV  string
		rightCSV string
		args     []string
		wantCode int
	}{
		{
			name:     "clean run exits 0",
			leftCSV:  "date,amount,reference\n2024-01-01,1.00,ref-1\n",
			rightCSV: "date,amount,reference\n2024-01-01,1.00,ref-1\n",
			args:     []string{"--fail-if-exceptions"},
			wantCode: 0,
		},
		{
			name:     "amount_diff exits 4",
			leftCSV:  "date,amount,reference\n2024-01-01,2.00,ref-1\n",
			rightCSV: "date,amount,reference\n2024-01-01,1.00,ref-1\n",
			args:     []string{"--fail-if-exceptions"},
			wantCode: 4,
		},
		{
			name:     "unmatched superset exits 4",
			leftCSV:  "date,amount,reference\n2024-01-01,1.00,ref-1\n2024-01-01,1.00,ref-only-l\n",
			rightCSV: "date,amount,reference\n2024-01-01,1.00,ref-1\n",
			args:     []string{"--fail-if-exceptions"},
			wantCode: 4,
		},
		{
			name:     "both flags unmatched -> code 4 precedence over 3",
			leftCSV:  "date,amount,reference\n2024-01-01,1.00,ref-1\n2024-01-01,1.00,ref-only-l\n",
			rightCSV: "date,amount,reference\n2024-01-01,1.00,ref-1\n",
			args:     []string{"--fail-if-unmatched", "--fail-if-exceptions"},
			wantCode: 4,
		},
		{
			name:     "fail-if-unmatched amount_diff only exits 0",
			leftCSV:  "date,amount,reference\n2024-01-01,2.00,ref-1\n",
			rightCSV: "date,amount,reference\n2024-01-01,1.00,ref-1\n",
			args:     []string{"--fail-if-unmatched"},
			wantCode: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			leftPath := filepath.Join(dir, "left.csv")
			rightPath := filepath.Join(dir, "right.csv")
			configPath := filepath.Join(dir, "reconify.yaml")
			resultPath := filepath.Join(dir, "result.ndjson")
			if err := os.WriteFile(leftPath, []byte(tc.leftCSV), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(rightPath, []byte(tc.rightCSV), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(configPath, []byte(buildTestConfig(leftPath, rightPath)), 0o600); err != nil {
				t.Fatal(err)
			}

			args := append([]string{"reconcile",
				"--config", configPath, "--pair", "pair",
				"--format", "ndjson", "--out", resultPath,
			}, tc.args...)
			// #nosec G204 -- test invokes the locally built CLI binary with fixed arguments.
			cmd := exec.Command(bin, args...)
			out, err := cmd.CombinedOutput()
			code := 0
			if err != nil {
				var exitErr *exec.ExitError
				if errors.As(err, &exitErr) {
					code = exitErr.ExitCode()
				} else {
					t.Fatalf("unexpected non-ExitError: %v\n%s", err, out)
				}
			}
			if code != tc.wantCode {
				t.Errorf("exit code = %d, want %d\noutput: %s", code, tc.wantCode, out)
			}
		})
	}
}
