//nolint:staticcheck // Domain aliases keep package-internal signatures readable.
package reconcile

import (
	"bytes"
	"context"
	"fmt"

	. "github.com/reconifyhq/reconify/engine/domain"

	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/reconifyhq/reconify/config"
	engineTelemetry "github.com/reconifyhq/reconify/engine/telemetry"
)

func TestReconcileStreamingWithTelemetryLifecycle(t *testing.T) {
	dir := t.TempDir()
	leftPath, rightPath := filepath.Join(dir, "left.csv"), filepath.Join(dir, "right.csv")
	csv := "date,amount,reference,name\n2024-01-01,1.00,ref-1,one\n2024-01-01,2.00,ref-2,two\n"
	if err := os.WriteFile(leftPath, []byte(csv), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rightPath, []byte(csv), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.CSVParserCfg{Type: "csv", DateCol: "date", DateLayout: "2006-01-02", AmountCol: "amount", Multiplier: 100, RefCol: "reference", NameCol: "name"}
	var events []TelemetryEvent
	idx := NewMemoryIndex()
	defer idx.Close()
	w, err := NewResultWriter("ndjson", &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if err := ReconcileStreamingWithTelemetry(context.Background(), "pair", "left", "right", leftPath, rightPath, cfg, cfg, config.Pair{DateWindow: "0d"}, idx, w, 0, TelemetryOptions{
		RunID: "test-run", ProgressEvery: 1,
		Sink: func(event TelemetryEvent) error {
			events = append(events, event)
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}

	completed := map[string]bool{}
	for _, event := range events {
		if event.RunID != "test-run" || event.Timestamp.IsZero() {
			t.Fatalf("invalid event identity: %#v", event)
		}
		if event.HeapBytes == nil || event.GCCycles == nil {
			t.Fatalf("runtime resource metrics must be available: %#v", event)
		}
		if event.Status == "completed" {
			completed[event.Stage] = true
		}
	}
	for _, stage := range []string{"right_index", "left_match", "finalization"} {
		if !completed[stage] {
			t.Errorf("missing completed %s event; got %#v", stage, events)
		}
	}
}

func TestReconcileWithTelemetryIncludesKnownTotalsAndETA(t *testing.T) {
	left := []Transaction{makeTx("left-1", 100, "ref")}
	right := []Transaction{makeTx("right-1", 100, "ref")}
	var events []TelemetryEvent
	_, err := ReconcileWithTelemetry("pair", "left", "right", left, right, config.Pair{DateWindow: "0d"}, TelemetryOptions{
		ProgressEvery: 1,
		Sink: func(event TelemetryEvent) error {
			events = append(events, event)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Stage == "left_match" && event.Status == "completed" {
			if event.TotalRows == nil || *event.TotalRows != 1 || event.Percentage == nil || *event.Percentage != 100 {
				t.Fatalf("known totals missing from event: %#v", event)
			}
			return
		}
	}
	t.Fatalf("missing left_match completion event: %#v", events)
}

func TestReconcileWithTelemetryReportsFailureInsteadOfCompletion(t *testing.T) {
	left := []Transaction{makeTx("left-1", 100, "ref")}
	right := []Transaction{makeTx("right-1", 100, "ref")}
	right[0].Currency = "EUR"
	var events []TelemetryEvent
	_, err := ReconcileWithTelemetry("pair", "left", "right", left, right, config.Pair{DateWindow: "0d"}, TelemetryOptions{
		Sink: func(event TelemetryEvent) error {
			events = append(events, event)
			return nil
		},
	})
	if err == nil {
		t.Fatal("expected reconciliation failure")
	}
	for _, event := range events {
		if event.Stage != "left_match" {
			continue
		}
		if event.Status == "completed" {
			t.Fatalf("failed reconciliation emitted a completed stage: %#v", events)
		}
		if event.Status == "failed" {
			return
		}
	}
	t.Fatalf("missing failed telemetry event: %#v", events)
}

func TestParseWithTelemetryEmitsParsingLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.csv")
	if err := os.WriteFile(path, []byte("date,amount,reference\n2024-01-01,1.00,ref-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.ParserCfg{Type: "csv", DateCol: "date", DateLayout: "2006-01-02", AmountCol: "amount", Multiplier: 100, RefCol: "reference"}
	var events []TelemetryEvent
	transactions, err := ParseWithTelemetry(context.Background(), "left", path, cfg, "right", TelemetryOptions{
		ProgressEvery: 1,
		Sink: func(event TelemetryEvent) error {
			events = append(events, event)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(transactions) != 1 {
		t.Fatalf("transactions = %d, want 1", len(transactions))
	}
	for _, event := range events {
		if event.Stage == "parse" && event.Status == "completed" && event.Rows == 1 {
			return
		}
	}
	t.Fatalf("missing completed parse event: %#v", events)
}

func TestTelemetrySinkFailureDoesNotAlterReconciliation(t *testing.T) {
	left := []Transaction{makeTx("left-1", 100, "ref")}
	right := []Transaction{makeTx("right-1", 100, "ref")}
	failures := 0
	result, err := ReconcileWithTelemetry("pair", "left", "right", left, right, config.Pair{DateWindow: "0d"}, TelemetryOptions{
		Sink:    func(TelemetryEvent) error { return fmt.Errorf("disk full") },
		OnError: func(error) { failures++ },
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary.MatchedCount != 1 {
		t.Fatalf("telemetry changed reconciliation result: %#v", result.Summary)
	}
	if failures != 1 {
		t.Fatalf("telemetry failure reports = %d, want 1", failures)
	}
}

func TestTelemetryHeartbeatUsesWallClockCadence(t *testing.T) {
	var events []TelemetryEvent
	reporter := engineTelemetry.NewReporter(TelemetryOptions{
		HeartbeatEvery: time.Millisecond,
		Sink: func(event TelemetryEvent) error {
			events = append(events, event)
			return nil
		},
	})
	reporter.Start("right_index", "right", "right", nil)
	time.Sleep(5 * time.Millisecond)
	reporter.Close()
	for _, event := range events {
		if event.Type == "heartbeat" {
			return
		}
	}
	t.Fatalf("expected heartbeat event, got %#v", events)
}

func TestTelemetryReporterFinishesNestedStagesExactlyOnce(t *testing.T) {
	var events []TelemetryEvent
	reporter := engineTelemetry.NewReporter(TelemetryOptions{
		Sink: func(event TelemetryEvent) error {
			events = append(events, event)
			return nil
		},
	})
	reporter.Start("parent", "left", "right", nil)
	reporter.Progress(3)
	reporter.Start("child", "right", "right", nil)
	reporter.Progress(2)
	reporter.Fail(0)
	reporter.Fail(99)
	reporter.Close()

	terminals := make(map[string][]TelemetryEvent)
	for _, event := range events {
		if event.Status == "completed" || event.Status == "failed" {
			terminals[event.Stage] = append(terminals[event.Stage], event)
		}
	}
	if len(terminals["child"]) != 1 || terminals["child"][0].Status != "failed" || terminals["child"][0].Rows != 2 {
		t.Fatalf("child terminal events = %#v", terminals["child"])
	}
	if len(terminals["parent"]) != 1 || terminals["parent"][0].Status != "failed" || terminals["parent"][0].Rows != 2 {
		t.Fatalf("parent terminal events = %#v", terminals["parent"])
	}
}

func TestReconcileStreamingWithTelemetryReportsParserFailure(t *testing.T) {
	dir := t.TempDir()
	leftPath := filepath.Join(dir, "left.csv")
	rightPath := filepath.Join(dir, "right.csv")
	if err := os.WriteFile(leftPath, []byte("date,amount,reference\n2024-01-01,100,ref-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rightPath, []byte("date,amount,reference\n2024-01-01,100,ref-1\n2024-01-01,not-an-amount,ref-2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.CSVParserCfg{Type: "csv", DateCol: "date", DateLayout: "2006-01-02", AmountCol: "amount", Multiplier: 1, RefCol: "reference"}
	var events []TelemetryEvent
	idx := NewMemoryIndex()
	defer idx.Close()
	err := ReconcileStreamingWithTelemetry(context.Background(), "pair", "left", "right", leftPath, rightPath, cfg, cfg, config.Pair{DateWindow: "0d"}, idx, &captureWriter{}, 0, TelemetryOptions{
		ProgressEvery: 1,
		Sink: func(event TelemetryEvent) error {
			events = append(events, event)
			return nil
		},
	})
	if err == nil {
		t.Fatal("expected parser failure")
	}
	event := findTelemetryTerminal(events, "right_index")
	if event == nil || event.Status != "failed" || event.Rows != 1 {
		t.Fatalf("right_index failure = %#v; events=%#v", event, events)
	}
}

func TestReconcileStreamingWithTelemetryReportsCancellation(t *testing.T) {
	dir := t.TempDir()
	leftPath := filepath.Join(dir, "left.csv")
	rightPath := filepath.Join(dir, "right.csv")
	content := []byte("date,amount,reference\n2024-01-01,100,ref-1\n")
	if err := os.WriteFile(leftPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rightPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.CSVParserCfg{Type: "csv", DateCol: "date", DateLayout: "2006-01-02", AmountCol: "amount", Multiplier: 1, RefCol: "reference"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var events []TelemetryEvent
	idx := NewMemoryIndex()
	defer idx.Close()
	err := ReconcileStreamingWithTelemetry(ctx, "pair", "left", "right", leftPath, rightPath, cfg, cfg, config.Pair{DateWindow: "0d"}, idx, &captureWriter{}, 0, TelemetryOptions{
		Sink: func(event TelemetryEvent) error {
			events = append(events, event)
			return nil
		},
	})
	if err == nil {
		t.Fatal("expected cancellation failure")
	}
	event := findTelemetryTerminal(events, "right_index")
	if event == nil || event.Status != "failed" {
		t.Fatalf("cancellation telemetry = %#v; events=%#v", event, events)
	}
}

func TestReconcileStreamingWithTelemetryReportsDuplicateScanFailure(t *testing.T) {
	dir := t.TempDir()
	leftPath := filepath.Join(dir, "left.csv")
	rightPath := filepath.Join(dir, "right.csv")
	if err := os.WriteFile(leftPath, []byte("date,amount,reference\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rightPath, []byte("date,amount,reference\n2024-01-01,100,ref-1\n2024-01-01,100,ref-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.CSVParserCfg{Type: "csv", DateCol: "date", DateLayout: "2006-01-02", AmountCol: "amount", Multiplier: 1, RefCol: "reference"}
	var events []TelemetryEvent
	idx := NewMemoryIndex()
	defer idx.Close()
	err := ReconcileStreamingWithTelemetry(context.Background(), "pair", "left", "right", leftPath, rightPath, cfg, cfg, config.Pair{DateWindow: "0d"}, idx, &failingTelemetryWriter{failOperation: "duplicate"}, 0, TelemetryOptions{
		ProgressEvery: 1,
		Sink: func(event TelemetryEvent) error {
			events = append(events, event)
			return nil
		},
	})
	if err == nil {
		t.Fatal("expected duplicate scan failure")
	}
	event := findTelemetryTerminal(events, "right_duplicate_scan")
	if event == nil || event.Status != "failed" || event.Rows != 2 {
		t.Fatalf("duplicate scan failure = %#v; events=%#v", event, events)
	}
}

func TestReconcileStreamingWithTelemetryReportsTokenMatchFailure(t *testing.T) {
	dir := t.TempDir()
	leftPath := filepath.Join(dir, "left.csv")
	rightPath := filepath.Join(dir, "right.csv")
	content := []byte("date,amount,reference,name\n2024-01-01,100,,Alice Smith\n")
	if err := os.WriteFile(leftPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rightPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.CSVParserCfg{Type: "csv", DateCol: "date", DateLayout: "2006-01-02", AmountCol: "amount", Multiplier: 1, RefCol: "reference", NameCol: "name"}
	var events []TelemetryEvent
	idx := NewMemoryIndex()
	defer idx.Close()
	err := ReconcileStreamingWithTelemetry(context.Background(), "pair", "left", "right", leftPath, rightPath, cfg, cfg, config.Pair{DateWindow: "0d", NameMode: "tokens"}, idx, &failingTelemetryWriter{failOperation: "match"}, 0, TelemetryOptions{
		ProgressEvery: 1,
		Sink: func(event TelemetryEvent) error {
			events = append(events, event)
			return nil
		},
	})
	if err == nil {
		t.Fatal("expected token match failure")
	}
	event := findTelemetryTerminal(events, "token_match")
	if event == nil || event.Status != "failed" || event.Rows != 2 {
		t.Fatalf("token match failure = %#v; events=%#v", event, events)
	}
}

func TestReconcileStreamingWithTelemetryReportsFinalizationFailure(t *testing.T) {
	dir := t.TempDir()
	leftPath := filepath.Join(dir, "left.csv")
	rightPath := filepath.Join(dir, "right.csv")
	content := []byte("date,amount,reference\n2024-01-01,100,ref-1\n")
	if err := os.WriteFile(leftPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rightPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.CSVParserCfg{Type: "csv", DateCol: "date", DateLayout: "2006-01-02", AmountCol: "amount", Multiplier: 1, RefCol: "reference"}
	var events []TelemetryEvent
	idx := NewMemoryIndex()
	defer idx.Close()
	err := ReconcileStreamingWithTelemetry(context.Background(), "pair", "left", "right", leftPath, rightPath, cfg, cfg, config.Pair{DateWindow: "0d"}, idx, &failingTelemetryWriter{failOperation: "summary"}, 0, TelemetryOptions{
		ProgressEvery: 1,
		Sink: func(event TelemetryEvent) error {
			events = append(events, event)
			return nil
		},
	})
	if err == nil {
		t.Fatal("expected finalization failure")
	}
	event := findTelemetryTerminal(events, "finalization")
	if event == nil || event.Status != "failed" || event.Rows != 2 {
		t.Fatalf("finalization failure = %#v; events=%#v", event, events)
	}
	if countTelemetryTerminals(events, "finalization") != 1 {
		t.Fatalf("finalization terminal events = %d; events=%#v", countTelemetryTerminals(events, "finalization"), events)
	}
}

func TestReconcileStreamingMultiSourceWithTelemetryReportsFailure(t *testing.T) {
	dir := t.TempDir()
	leftPath := filepath.Join(dir, "left.csv")
	rightPath := filepath.Join(dir, "right.csv")
	content := []byte("date,amount,reference\n2024-01-01,100,ref-1\n")
	if err := os.WriteFile(leftPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rightPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.CSVParserCfg{Type: "csv", DateCol: "date", DateLayout: "2006-01-02", AmountCol: "amount", Multiplier: 1, RefCol: "reference"}
	idx := NewMemoryIndex()
	defer idx.Close()
	var events []TelemetryEvent
	err := ReconcileStreamingMultiSourceWithTelemetry(context.Background(), "pair", "left", leftPath, cfg, []CounterpartStream{{SourceName: "right", RightPath: rightPath, RightCfg: cfg, Index: idx}}, config.Pair{DateWindow: "0d"}, &failingTelemetryWriter{failOperation: "summary"}, 0, TelemetryOptions{
		ProgressEvery: 1,
		Sink: func(event TelemetryEvent) error {
			events = append(events, event)
			return nil
		},
	})
	if err == nil {
		t.Fatal("expected multi-source finalization failure")
	}
	event := findTelemetryTerminal(events, "finalization")
	if event == nil || event.Status != "failed" || event.Rows != 2 {
		t.Fatalf("multi-source finalization failure = %#v; events=%#v", event, events)
	}
}

func TestReconcilePartitionedWithTelemetryReportsFinalizationFailure(t *testing.T) {
	dir := t.TempDir()
	leftPath := filepath.Join(dir, "left.csv")
	rightPath := filepath.Join(dir, "right.csv")
	content := []byte("date,amount,reference\n2024-01-01,100,ref-1\n")
	if err := os.WriteFile(leftPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rightPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.ParserCfg{Type: "csv", DateCol: "date", DateLayout: "2006-01-02", AmountCol: "amount", Multiplier: 1, RefCol: "reference"}
	var events []TelemetryEvent
	err := ReconcilePartitionedWithOptionsAndTelemetry(context.Background(), "pair", "left", "right", leftPath, rightPath, cfg, cfg, config.Pair{DateWindow: "0d"}, &failingTelemetryWriter{failOperation: "summary"}, PartitionedOptions{Partitions: 2, SpillDir: filepath.Join(dir, "spill")}, TelemetryOptions{
		ProgressEvery: 1,
		Sink: func(event TelemetryEvent) error {
			events = append(events, event)
			return nil
		},
	})
	if err == nil {
		t.Fatal("expected partitioned finalization failure")
	}
	event := findTelemetryTerminal(events, "finalization")
	if event == nil || event.Status != "failed" || event.Rows != 2 {
		t.Fatalf("partitioned finalization failure = %#v; events=%#v", event, events)
	}
}

func TestReconcilePartitionedMultiSourceWithTelemetryPreservesFailureRows(t *testing.T) {
	dir := t.TempDir()
	leftPath := filepath.Join(dir, "left.csv")
	rightPath := filepath.Join(dir, "right.csv")
	content := []byte("date,amount,reference\n2024-01-01,100,ref-1\n")
	if err := os.WriteFile(leftPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rightPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.ParserCfg{Type: "csv", DateCol: "date", DateLayout: "2006-01-02", AmountCol: "amount", Multiplier: 1, RefCol: "reference"}
	var events []TelemetryEvent
	err := ReconcilePartitionedMultiSourceWithOptionsAndTelemetry(context.Background(), "pair", "left", leftPath, cfg, []PartitionedCounterpartInput{{SourceName: "right", RightPath: rightPath, ParserCfg: cfg}}, config.Pair{DateWindow: "0d"}, &failingTelemetryWriter{failOperation: "summary"}, PartitionedOptions{Partitions: 2, SpillDir: filepath.Join(dir, "spill")}, TelemetryOptions{
		ProgressEvery: 1,
		Sink: func(event TelemetryEvent) error {
			events = append(events, event)
			return nil
		},
	})
	if err == nil {
		t.Fatal("expected partitioned multi-source finalization failure")
	}
	event := findTelemetryTerminal(events, "finalization")
	if event == nil || event.Status != "failed" || event.Rows != 2 {
		t.Fatalf("partitioned multi-source finalization failure = %#v; events=%#v", event, events)
	}
}

func findTelemetryTerminal(events []TelemetryEvent, stage string) *TelemetryEvent {
	var first *TelemetryEvent
	for i := range events {
		if events[i].Stage == stage && (events[i].Status == "completed" || events[i].Status == "failed") {
			if first == nil {
				first = &events[i]
			}
			if events[i].Status == "failed" {
				return &events[i]
			}
		}
	}
	return first
}

func countTelemetryTerminals(events []TelemetryEvent, stage string) int {
	count := 0
	for _, event := range events {
		if event.Stage == stage && (event.Status == "completed" || event.Status == "failed") {
			count++
		}
	}
	return count
}

type failingTelemetryWriter struct {
	captureWriter
	failOperation string
}

func (w *failingTelemetryWriter) WriteMatch(pair MatchedPair) error {
	if w.failOperation == "match" {
		return fmt.Errorf("write match failed")
	}
	return w.captureWriter.WriteMatch(pair)
}

func (w *failingTelemetryWriter) WriteSummary(summary Summary) error {
	if w.failOperation == "summary" {
		return fmt.Errorf("write summary failed")
	}
	return w.captureWriter.WriteSummary(summary)
}

func (w *failingTelemetryWriter) WriteDuplicate(group DuplicateGroup) error {
	if w.failOperation == "duplicate" {
		return fmt.Errorf("write duplicate failed")
	}
	return w.captureWriter.WriteDuplicate(group)
}

func (w *failingTelemetryWriter) Flush() error {
	if w.failOperation == "flush" {
		return fmt.Errorf("flush failed")
	}
	return w.captureWriter.Flush()
}
