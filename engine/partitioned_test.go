package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/reconifyhq/reconify/config"
)

func TestReconcilePartitionedMatchesStreamingResults(t *testing.T) {
	dir := t.TempDir()
	left := filepath.Join(dir, "left.csv")
	right := filepath.Join(dir, "right.csv")
	write := func(path string, n int) {
		f, err := os.Create(path) // #nosec G304 -- test path is created under t.TempDir().
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		fmt.Fprintln(f, "date,amount,currency,reference")
		for i := 0; i < n; i++ {
			fmt.Fprintf(f, "2026-01-01,%d,USD,REF-%03d\n", 100+i, i)
		}
	}
	write(left, 100)
	write(right, 90)
	cfg := config.ParserCfg{Type: "csv", DateCol: "date", DateLayout: "2006-01-02", AmountCol: "amount", CurrencyCol: "currency", RefCol: "reference", SkipRaw: true}
	pair := config.Pair{Left: "left", Right: "right"}

	var baseline bytes.Buffer
	bw, err := NewResultWriter("ndjson", &baseline)
	if err != nil {
		t.Fatal(err)
	}
	idx := NewMemoryIndex()
	if err := ReconcileStreaming(context.Background(), "p", "left", "right", left, right, cfg, cfg, pair, idx, bw, 0); err != nil {
		t.Fatal(err)
	}
	if err := idx.Close(); err != nil {
		t.Fatal(err)
	}

	var partitioned bytes.Buffer
	pw, err := NewResultWriter("ndjson", &partitioned)
	if err != nil {
		t.Fatal(err)
	}
	if err := ReconcilePartitioned(context.Background(), "p", "left", "right", left, right, cfg, cfg, pair, pw, 0, 4); err != nil {
		t.Fatal(err)
	}
	baseSummary := lastSummaryLine(baseline.String())
	partSummary := lastSummaryLine(partitioned.String())
	if baseSummary != partSummary {
		t.Fatalf("summary mismatch\nbaseline: %s\npartitioned: %s", baseSummary, partSummary)
	}
	baseEvents := sortedEventLines(baseline.String())
	partitionedEvents := sortedEventLines(partitioned.String())
	if strings.Join(baseEvents, "\n") != strings.Join(partitionedEvents, "\n") {
		t.Fatalf("event output mismatch\nbaseline: %s\npartitioned: %s", strings.Join(baseEvents, "\n"), strings.Join(partitionedEvents, "\n"))
	}
}

func lastSummaryLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(lines[i], `"type":"summary"`) {
			return lines[i]
		}
	}
	return ""
}

func sortedEventLines(s string) []string {
	var events []string
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		var event struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil || event.Type == "summary" || event.Type == "" {
			continue
		}
		var value any
		if err := json.Unmarshal([]byte(line), &value); err != nil {
			continue
		}
		// Partition files restart parser row numbers, so generated synthetic IDs
		// differ by partition. Business fields and event types remain comparable.
		stripGeneratedIDs(value)
		canonical, err := json.Marshal(value)
		if err == nil {
			events = append(events, string(canonical))
		}
	}
	sort.Strings(events)
	return events
}

func stripGeneratedIDs(value any) {
	switch v := value.(type) {
	case map[string]any:
		delete(v, "id")
		for _, child := range v {
			stripGeneratedIDs(child)
		}
	case []any:
		for _, child := range v {
			stripGeneratedIDs(child)
		}
	}
}
