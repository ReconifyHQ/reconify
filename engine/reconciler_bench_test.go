package engine

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/reconify/reconify/config"
)

// benchmarkReconcileStreaming creates two synthetic CSV files (left and right)
// and benchmarks ReconcileStreaming end-to-end with ndjson (O(1)) output.
// Both sides are identical (100% reference match rate) — this is the
// best-case scenario measuring pure CPU + hash throughput with warm OS cache.
// For a realistic mixed-rate benchmark see BenchmarkReconcileStreaming_20M_Realistic.
func benchmarkReconcileStreaming(b *testing.B, rows int) {
	leftPath  := writeSyntheticCSV(b, rows)
	rightPath := writeSyntheticCSV(b, rows)

	cfg := config.CSVParserCfg{
		Type:        "csv",
		DateCol:     "date",
		DateLayout:  "2006-01-02",
		AmountCol:   "amount",
		Multiplier:  100,
		CurrencyCol: "currency",
		RefCol:      "reference",
		NameCol:     "name",
		SkipRaw:     true,
	}

	pair := config.Pair{
		Left:                 "left",
		Right:                "right",
		AmountToleranceMinor: 0,
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		idx := NewMemoryIndex()
		w, _ := NewResultWriter("ndjson", io.Discard)

		if err := ReconcileStreaming(
			context.Background(),
			"bench_pair",
			"left", "right",
			leftPath, rightPath,
			cfg, cfg,
			pair,
			idx, w,
			100_000,
		); err != nil {
			b.Fatal(err)
		}
		idx.Close()
		b.ReportMetric(float64(rows), "rows/op")
	}
}

func BenchmarkReconcileStreaming_100k(b *testing.B) { benchmarkReconcileStreaming(b, 100_000) }
func BenchmarkReconcileStreaming_1M(b *testing.B)   { benchmarkReconcileStreaming(b, 1_000_000) }

// ─────────────────────────────────────────────────────────────────────────────
// Large-file benchmarks (require pre-generated fixture data)
//
// These benchmarks use the 20M-row CSV files produced by gen_bench_data.go.
// They are skipped automatically when BENCH_DATA_DIR is not set, so they
// never block normal `go test ./...` runs.
//
// Generate data once, then run:
//
//	go run scripts/gen_bench_data.go --rows 20000000 --out /tmp/bench-data
//	BENCH_DATA_DIR=/tmp/bench-data go test -run='^$' \
//	  -bench='BenchmarkReconcileStreaming_20M|BenchmarkParseCSVEach_20M' \
//	  -benchtime=1x -benchmem -timeout=600s ./engine/...
//
// Use -benchtime=1x because a single 20M-row reconciliation takes ~60–120 s;
// the benchmark framework's auto-calibration would run it many times otherwise.
//
// What the fixtures measure (vs. the synthetic benchmarks above):
//   - Different column names on left vs right (exercises field mapping)
//   - Mixed outcome distribution: 85% matched, 5% amount diff, 5% timing diff,
//     5% unmatched left, 5% unmatched right  (vs 100% match above)
//   - File size ~1.2 GB per side — likely exceeds L3 cache, so OS I/O is real
// ─────────────────────────────────────────────────────────────────────────────

// largeFileCfgs returns the CSVParserCfg for left.csv and right.csv as generated
// by scripts/gen_bench_data.go. Column names differ between the two files.
func largeFileCfgs() (left, right config.CSVParserCfg) {
	left = config.CSVParserCfg{
		Type:        "csv",
		DateCol:     "date",
		DateLayout:  "2006-01-02",
		AmountCol:   "amount",
		Multiplier:  1,
		CurrencyCol: "currency",
		RefCol:      "ref_id",
		NameCol:     "description",
		SkipRaw:     true,
	}
	right = config.CSVParserCfg{
		Type:        "csv",
		DateCol:     "txn_date",
		DateLayout:  "2006-01-02",
		AmountCol:   "txn_amount",
		Multiplier:  1,
		CurrencyCol: "txn_currency",
		RefCol:      "txn_ref",
		NameCol:     "merchant",
		SkipRaw:     true,
	}
	return left, right
}

// requireBenchData returns the path to left.csv and right.csv from BENCH_DATA_DIR,
// or skips the benchmark with an actionable message if the env var is unset.
func requireBenchData(b *testing.B) (leftPath, rightPath string) {
	b.Helper()
	dataDir := os.Getenv("BENCH_DATA_DIR")
	if dataDir == "" {
		b.Skip("BENCH_DATA_DIR not set — generate data first:\n" +
			"  go run scripts/gen_bench_data.go --rows 20000000 --out /tmp/bench-data\n" +
			"  export BENCH_DATA_DIR=/tmp/bench-data")
	}
	leftPath  = filepath.Join(dataDir, "left.csv")
	rightPath = filepath.Join(dataDir, "right.csv")
	for _, p := range []string{leftPath, rightPath} {
		if _, err := os.Stat(p); err != nil {
			b.Skipf("file not found: %s (re-run gen_bench_data.go)", p)
		}
	}
	return leftPath, rightPath
}

// BenchmarkParseCSVEach_20M_Left measures streaming parse throughput on a real
// 20M-row CSV file with the bank/ledger column schema.
// Baseline: ~1.5–2.0 M rows/sec on NVMe with warm OS cache.
func BenchmarkParseCSVEach_20M_Left(b *testing.B) {
	leftPath, _ := requireBenchData(b)
	leftCfg, _  := largeFileCfgs()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		count := 0
		if err := ParseCSVEach(context.Background(), "bank", leftPath, leftCfg,
			func(_ Transaction, _ int) error {
				count++
				return nil
			},
		); err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(count), "rows/op")
	}
}

// BenchmarkReconcileStreaming_20M_Realistic measures end-to-end reconciliation
// of two 20M-row CSV files with a realistic mixed outcome distribution:
//   - 85% perfect matches (same ref, amount, date)
//   - 5%  amount diffs   (right amount +1–50 minor units)
//   - 5%  timing diffs   (right date +1–3 days)
//   - 5%  unmatched left
//   - 5%  unmatched right
//
// The two files use different column schemas to exercise CSVParserCfg mapping.
// Output is written to io.Discard (ndjson) so writer overhead is minimal.
//
// Run with -benchtime=1x; a single iteration takes ~60–120 s.
// Peak RSS: the right-side memoryIndex holds ~20M rows × ~200 bytes ≈ 4 GB.
func BenchmarkReconcileStreaming_20M_Realistic(b *testing.B) {
	leftPath, rightPath := requireBenchData(b)
	leftCfg, rightCfg  := largeFileCfgs()

	pair := config.Pair{
		Left:                 "bank",
		Right:                "processor",
		AmountToleranceMinor: 0,
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		idx := NewMemoryIndex()
		w, _ := NewResultWriter("ndjson", io.Discard)

		if err := ReconcileStreaming(
			context.Background(),
			"bench_20m",
			"bank", "processor",
			leftPath, rightPath,
			leftCfg, rightCfg,
			pair,
			idx, w,
			0,
		); err != nil {
			b.Fatal(err)
		}
		idx.Close()
		b.ReportMetric(20_000_000, "rows/op")
	}
}
