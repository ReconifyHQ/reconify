package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/reconifyhq/reconify/config"
)

func writeSelectionCSV(t *testing.T, path string, rows ...string) {
	t.Helper()
	content := "date,amount,reference\n2026-01-01,100,REF-1\n"
	if len(rows) > 0 {
		content = "date,amount,Reference\n" + strings.Join(rows, "\n") + "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func selectionParser() config.ParserCfg {
	return config.ParserCfg{Type: "csv", DateCol: "date", DateLayout: "2006-01-02", AmountCol: "amount", Multiplier: 1, RefCol: "Reference"}
}

func withFreeDisk(t *testing.T, bytes int64, err error) {
	t.Helper()
	previous := freeDiskBytes
	freeDiskBytes = func(string) (int64, error) { return bytes, err }
	t.Cleanup(func() { freeDiskBytes = previous })
}

func TestChooseIndexBackend_AutoPreservesThresholdWithoutBudgets(t *testing.T) {
	dir := t.TempDir()
	left := filepath.Join(dir, "left.csv")
	right := filepath.Join(dir, "right.csv")
	writeSelectionCSV(t, left)
	writeSelectionCSV(t, right)

	decision, err := chooseIndexBackend(config.IndexCfg{Backend: "auto", AutoMaxRightFileMB: 1}, left, right, selectionParser(), selectionParser(), config.Pair{}, 1)
	if err != nil {
		t.Fatalf("chooseIndexBackend: %v", err)
	}
	if decision.Selection.Backend != "memory" {
		t.Fatalf("backend=%q, want memory", decision.Selection.Backend)
	}
}

func TestChooseIndexBackend_ResourceBudgetFallsBackToDisk(t *testing.T) {
	dir := t.TempDir()
	left := filepath.Join(dir, "left.csv")
	right := filepath.Join(dir, "right.csv")
	writeSelectionCSV(t, left)
	writeSelectionCSV(t, right)
	withFreeDisk(t, 1<<40, nil)

	decision, err := chooseIndexBackend(config.IndexCfg{Backend: "auto", MaxMemoryMB: 128, MaxTempDiskMB: 1024}, left, right, selectionParser(), selectionParser(), config.Pair{}, 1)
	if err != nil {
		t.Fatalf("chooseIndexBackend: %v", err)
	}
	if decision.Selection.Backend != "disk" {
		t.Fatalf("backend=%q, want disk", decision.Selection.Backend)
	}
	if len(decision.Selection.Fallbacks) != 1 || decision.Selection.Fallbacks[0].Backend != "memory" {
		t.Fatalf("fallbacks=%+v, want memory rejection", decision.Selection.Fallbacks)
	}
}

func TestChooseIndexBackend_DiskUnavailableFailsExplicitly(t *testing.T) {
	dir := t.TempDir()
	right := filepath.Join(dir, "right.csv")
	writeSelectionCSV(t, right)
	withFreeDisk(t, 0, nil)

	_, err := chooseIndexBackend(config.IndexCfg{Backend: "disk"}, "", right, config.ParserCfg{}, selectionParser(), config.Pair{}, 1)
	if err == nil || !strings.Contains(err.Error(), "available temporary disk") {
		t.Fatalf("error=%v, want unavailable temporary disk error", err)
	}
}

func TestChooseIndexBackend_PartitionedAcceptsCaseInsensitiveReferenceHeader(t *testing.T) {
	dir := t.TempDir()
	left := filepath.Join(dir, "left.csv")
	right := filepath.Join(dir, "right.csv")
	writeSelectionCSV(t, left, "2026-01-01,100,REF-1")
	writeSelectionCSV(t, right, "2026-01-01,100,REF-1")
	withFreeDisk(t, 1<<40, nil)

	decision, err := chooseIndexBackend(config.IndexCfg{Backend: "partitioned", MaxMemoryMB: 1024, MaxTempDiskMB: 1024}, left, right, selectionParser(), selectionParser(), config.Pair{}, 1)
	if err != nil {
		t.Fatalf("chooseIndexBackend: %v", err)
	}
	if !decision.Partitioned || decision.Selection.Backend != "partitioned" {
		t.Fatalf("decision=%+v, want partitioned", decision)
	}
}

func TestChooseIndexBackend_AutoAggregatesResourceFailures(t *testing.T) {
	dir := t.TempDir()
	left := filepath.Join(dir, "left.csv")
	right := filepath.Join(dir, "right.csv")
	writeSelectionCSV(t, left)
	writeSelectionCSV(t, right)
	withFreeDisk(t, 0, nil)

	_, err := chooseIndexBackend(config.IndexCfg{Backend: "auto", MaxMemoryMB: 1, MaxTempDiskMB: 1}, left, right, selectionParser(), selectionParser(), config.Pair{}, 1)
	if err == nil || !strings.Contains(err.Error(), "no suitable index backend") || !strings.Contains(err.Error(), "memory:") || !strings.Contains(err.Error(), "disk:") || !strings.Contains(err.Error(), "partitioned:") {
		t.Fatalf("error=%v, want aggregated backend failures", err)
	}
}
