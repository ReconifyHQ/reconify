package engine

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/reconifyhq/reconify/config"
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
	reporter := newTelemetryReporter(TelemetryOptions{
		HeartbeatEvery: time.Millisecond,
		Sink: func(event TelemetryEvent) error {
			events = append(events, event)
			return nil
		},
	})
	reporter.start("right_index", "right", "right", nil)
	time.Sleep(5 * time.Millisecond)
	reporter.close()
	for _, event := range events {
		if event.Type == "heartbeat" {
			return
		}
	}
	t.Fatalf("expected heartbeat event, got %#v", events)
}
