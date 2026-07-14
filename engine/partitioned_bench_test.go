package engine

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/reconifyhq/reconify/config"
)

func writeGroupedBenchmarkCSV(b *testing.B, dir string, groups, rightsPerGroup int, skew bool) (leftPath, rightPath string) {
	b.Helper()
	leftPath = filepath.Join(dir, "left.csv")
	rightPath = filepath.Join(dir, "right.csv")
	left, err := os.Create(leftPath) // #nosec G304 -- benchmark fixture path is under b.TempDir().
	if err != nil {
		b.Fatal(err)
	}
	right, err := os.Create(rightPath) // #nosec G304 -- benchmark fixture path is under b.TempDir().
	if err != nil {
		b.Fatal(err)
	}
	_, _ = fmt.Fprintln(left, "date,amount,reference")
	_, _ = fmt.Fprintln(right, "date,amount,reference")
	if skew {
		_, _ = fmt.Fprintf(left, "2026-01-01,%d,SKEW\n", rightsPerGroup)
		for i := 0; i < rightsPerGroup; i++ {
			_, _ = fmt.Fprintln(right, "2026-01-01,1,SKEW")
		}
	} else {
		for i := 0; i < groups; i++ {
			ref := fmt.Sprintf("G-%08d", i)
			_, _ = fmt.Fprintf(left, "2026-01-01,%d,%s\n", rightsPerGroup*100, ref)
			for j := 0; j < rightsPerGroup; j++ {
				_, _ = fmt.Fprintf(right, "2026-01-01,100,%s\n", ref)
			}
		}
	}
	if err := left.Close(); err != nil {
		b.Fatal(err)
	}
	if err := right.Close(); err != nil {
		b.Fatal(err)
	}
	return leftPath, rightPath
}

func groupedBenchmarkConfig() config.ParserCfg {
	return config.ParserCfg{
		Type:       "csv",
		DateCol:    "date",
		DateLayout: "2006-01-02",
		AmountCol:  "amount",
		RefCol:     "reference",
		SkipRaw:    true,
	}
}

func groupedBenchmarkPair() config.Pair {
	return config.Pair{Passes: []config.PassConfig{{Type: config.PassTypeOneToMany}}}
}

func BenchmarkReconcilePartitioned_OneToManyBalanced(b *testing.B) {
	dir := b.TempDir()
	leftPath, rightPath := writeGroupedBenchmarkCSV(b, dir, 5_000, 3, false)
	cfg := groupedBenchmarkConfig()
	pair := groupedBenchmarkPair()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		writer, err := NewResultWriter("ndjson", io.Discard)
		if err != nil {
			b.Fatal(err)
		}
		if err := ReconcilePartitioned(context.Background(), "bench", "left", "right", leftPath, rightPath, cfg, cfg, pair, writer, 0, 8); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkReconcileBatch_OneToManyBalanced(b *testing.B) {
	dir := b.TempDir()
	leftPath, rightPath := writeGroupedBenchmarkCSV(b, dir, 5_000, 3, false)
	cfg := groupedBenchmarkConfig()
	pair := groupedBenchmarkPair()
	left, err := Parse("left", leftPath, cfg)
	if err != nil {
		b.Fatal(err)
	}
	right, err := Parse("right", rightPath, cfg)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Reconcile("bench", "left", "right", left, right, pair); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkReconcilePartitioned_OneToManySkewed(b *testing.B) {
	dir := b.TempDir()
	leftPath, rightPath := writeGroupedBenchmarkCSV(b, dir, 0, 20_000, true)
	cfg := groupedBenchmarkConfig()
	pair := groupedBenchmarkPair()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		writer, err := NewResultWriter("ndjson", io.Discard)
		if err != nil {
			b.Fatal(err)
		}
		if err := ReconcilePartitioned(context.Background(), "bench", "left", "right", leftPath, rightPath, cfg, cfg, pair, writer, 0, 8); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkReconcileBatch_OneToManySkewed(b *testing.B) {
	dir := b.TempDir()
	leftPath, rightPath := writeGroupedBenchmarkCSV(b, dir, 0, 20_000, true)
	cfg := groupedBenchmarkConfig()
	pair := groupedBenchmarkPair()
	left, err := Parse("left", leftPath, cfg)
	if err != nil {
		b.Fatal(err)
	}
	right, err := Parse("right", rightPath, cfg)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Reconcile("bench", "left", "right", left, right, pair); err != nil {
			b.Fatal(err)
		}
	}
}
