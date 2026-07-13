package engine

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/reconifyhq/reconify/config"
)

func TestReconcilePartitionedMatchesStreamingResults(t *testing.T) {
	dir := t.TempDir()
	left := filepath.Join(dir, "left.csv")
	right := filepath.Join(dir, "right.csv")
	write := func(path string, n int) {
		f, err := os.Create(path)
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
