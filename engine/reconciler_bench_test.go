package engine

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/reconifyhq/reconify/config"
)

// benchmarkReconcileStreaming creates two synthetic CSV files (left and right)
// and benchmarks ReconcileStreaming end-to-end with ndjson (O(1)) output.
// Both sides are identical (100% reference match rate) — this is the
// best-case scenario measuring pure CPU + hash throughput with warm OS cache.
// For a realistic mixed-rate benchmark see BenchmarkReconcileStreaming_20M_Realistic.
func benchmarkReconcileStreaming(b *testing.B, rows int) {
	leftPath := writeSyntheticCSV(b, rows)
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
		if err := idx.Close(); err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(rows), "left_rows/op")
		b.ReportMetric(float64(rows*2), "total_rows/op")
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
	leftPath = filepath.Join(dataDir, "left.csv")
	rightPath = filepath.Join(dataDir, "right.csv")
	for _, p := range []string{leftPath, rightPath} {
		if _, err := os.Stat(p); err != nil { // #nosec G703 -- BENCH_DATA_DIR is an explicit benchmark fixture directory.
			b.Skipf("file not found: %s (re-run gen_bench_data.go)", p)
		}
	}
	return leftPath, rightPath
}

// countCSVDataRows returns the number of data rows in a CSV file.
// The header row is excluded.
func countCSVDataRows(path string) (int, error) {
	f, err := os.Open(path) // #nosec G304 -- benchmark fixture path comes from BENCH_DATA_DIR.
	if err != nil {
		return 0, err
	}
	defer func() {
		if err := f.Close(); err != nil {
			_ = err
		}
	}()

	sc := bufio.NewScanner(f)
	// Default scanner max token is 64K. These fixture rows are much smaller,
	// but we lift the limit defensively for wider generated schemas.
	buf := make([]byte, 0, 1024*64)
	sc.Buffer(buf, 1024*1024)

	lines := 0
	for sc.Scan() {
		lines++
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}
	if lines == 0 {
		return 0, fmt.Errorf("empty CSV file: %s", path)
	}
	return lines - 1, nil
}

// BenchmarkParseCSVEach_20M_Left measures streaming parse throughput on a real
// 20M-row CSV file with the bank/ledger column schema.
// Baseline: ~1.5–2.0 M rows/sec on NVMe with warm OS cache.
func BenchmarkParseCSVEach_20M_Left(b *testing.B) {
	leftPath, _ := requireBenchData(b)
	leftCfg, _ := largeFileCfgs()

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
	leftCfg, rightCfg := largeFileCfgs()
	leftRows, err := countCSVDataRows(leftPath)
	if err != nil {
		b.Fatalf("count left rows: %v", err)
	}
	rightRows, err := countCSVDataRows(rightPath)
	if err != nil {
		b.Fatalf("count right rows: %v", err)
	}
	if leftRows != rightRows {
		b.Fatalf("row count mismatch: left=%d right=%d", leftRows, rightRows)
	}

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
		if err := idx.Close(); err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(leftRows), "left_rows/op")
		b.ReportMetric(float64(leftRows+rightRows), "total_rows/op")
	}
}

// benchmarkReconcilePartitionedLarge measures the disk-backed duplicate
// disposition path. Generate fixtures with --duplicate-groups to exercise the
// duplicate-group emission path; the benchmark remains env-gated so normal
// tests never require multi-gigabyte inputs.
func benchmarkReconcilePartitionedLarge(b *testing.B, expectedRows int) {
	leftPath, rightPath := requireBenchData(b)
	leftCfg, rightCfg := largeFileCfgs()
	leftCfg.GroupCol = "processor_hint"
	leftRows, err := countCSVDataRows(leftPath)
	if err != nil {
		b.Fatal(err)
	}
	if leftRows != expectedRows {
		b.Skipf("fixture has %d rows; this benchmark requires %d rows", leftRows, expectedRows)
	}

	pair := config.Pair{Left: "bank", Right: "processor", DateWindow: "0d"}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		w, _ := NewResultWriter("ndjson", io.Discard)
		if err := ReconcilePartitionedWithOptions(
			context.Background(), "bench_partitioned", "bank", "processor",
			leftPath, rightPath, leftCfg, rightCfg, pair, w,
			PartitionedOptions{Partitions: 64},
		); err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(leftRows), "left_rows/op")
	}
}

func BenchmarkReconcilePartitioned_5M_DuplicatePreScan(b *testing.B) {
	benchmarkReconcilePartitionedLarge(b, 5_000_000)
}

func BenchmarkReconcilePartitioned_20M_DuplicatePreScan(b *testing.B) {
	benchmarkReconcilePartitionedLarge(b, 20_000_000)
}

// ---------------------------------------------------------------------------
// one_to_many batch benchmarks
// ---------------------------------------------------------------------------

// makeGroupedTxns builds left and right slices for one_to_many benchmarking.
// Each left row gets rightsPerGroup right rows sharing the same reference.
// All amounts sum exactly; all dates match (no timing diff).
func makeGroupedTxns(leftCount, rightsPerGroup int) (left, right []Transaction) {
	date := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	left = make([]Transaction, leftCount)
	right = make([]Transaction, leftCount*rightsPerGroup)
	for i := 0; i < leftCount; i++ {
		ref := fmt.Sprintf("INV-%08d", i)
		installmentAmt := int64(1000)
		left[i] = Transaction{
			ID:        fmt.Sprintf("l%d", i),
			Date:      date,
			Amount:    installmentAmt * int64(rightsPerGroup),
			Currency:  "USD",
			Reference: ref,
			Source:    "left",
		}
		for j := 0; j < rightsPerGroup; j++ {
			right[i*rightsPerGroup+j] = Transaction{
				ID:        fmt.Sprintf("r%d-%d", i, j),
				Date:      date,
				Amount:    installmentAmt,
				Currency:  "USD",
				Reference: ref,
				Source:    "right",
			}
		}
	}
	return left, right
}

// BenchmarkReconcile_OneToMany_SmallGroups benchmarks 100k invoices each split
// into 3 installments (300k right rows). All groups resolve to GroupedMatched.
func BenchmarkReconcile_OneToMany_SmallGroups(b *testing.B) {
	const leftCount = 100_000
	const rightsPerGroup = 3
	left, right := makeGroupedTxns(leftCount, rightsPerGroup)
	pair := config.Pair{
		AmountToleranceMinor: 0,
		Passes:               []config.PassConfig{{Type: config.PassTypeOneToMany}},
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		res, err := Reconcile("bench", "left", "right", left, right, pair)
		if err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(leftCount), "left_rows/op")
		b.ReportMetric(float64(len(res.GroupedMatched)), "grouped_matched/op")
	}
}

// BenchmarkReconcile_OneToMany_LargeGroups benchmarks 10k invoices each split
// into 50 installments (500k right rows). Exercises larger group scans.
func BenchmarkReconcile_OneToMany_LargeGroups(b *testing.B) {
	const leftCount = 10_000
	const rightsPerGroup = 50
	left, right := makeGroupedTxns(leftCount, rightsPerGroup)
	pair := config.Pair{
		AmountToleranceMinor: 0,
		Passes:               []config.PassConfig{{Type: config.PassTypeOneToMany}},
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		res, err := Reconcile("bench", "left", "right", left, right, pair)
		if err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(leftCount), "left_rows/op")
		b.ReportMetric(float64(len(res.GroupedMatched)), "grouped_matched/op")
	}
}

// BenchmarkReconcile_OneToMany_MixedOutcomes benchmarks a realistic mix:
// 50k left rows split into exact matches, amount diffs, and ambiguous groups
// via a [reference_one_to_one, one_to_many] pipeline. Exercises pass handoff.
func BenchmarkReconcile_OneToMany_MixedOutcomes(b *testing.B) {
	const total = 50_000
	date := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	left := make([]Transaction, total)
	right := make([]Transaction, 0, total*2)

	for i := 0; i < total; i++ {
		ref := fmt.Sprintf("INV-%08d", i)
		left[i] = Transaction{
			ID: fmt.Sprintf("l%d", i), Date: date,
			Amount: 20000, Currency: "USD", Reference: ref, Source: "left",
		}
		switch i % 4 {
		case 0: // 1-to-1 exact match (consumed by reference_one_to_one pass)
			right = append(right, Transaction{
				ID: fmt.Sprintf("r%d", i), Date: date,
				Amount: 20000, Currency: "USD", Reference: ref, Source: "right",
			})
		case 1: // 2 installments summing exactly (GroupedMatched in one_to_many pass)
			right = append(right,
				Transaction{ID: fmt.Sprintf("r%d-a", i), Date: date, Amount: 10000, Currency: "USD", Reference: ref, Source: "right"},
				Transaction{ID: fmt.Sprintf("r%d-b", i), Date: date, Amount: 10000, Currency: "USD", Reference: ref, Source: "right"},
			)
		case 2: // 2 installments with amount diff (GroupedAmountDiff)
			right = append(right,
				Transaction{ID: fmt.Sprintf("r%d-a", i), Date: date, Amount: 8000, Currency: "USD", Reference: ref, Source: "right"},
				Transaction{ID: fmt.Sprintf("r%d-b", i), Date: date, Amount: 8000, Currency: "USD", Reference: ref, Source: "right"},
			)
		case 3: // ambiguous: 2 left rows share this reference — neither matched
			// add a duplicate left row (will be detected as ambiguous group)
			// use a different ID but same reference — insert via right only to keep 50k left
			right = append(right,
				Transaction{ID: fmt.Sprintf("r%d-a", i), Date: date, Amount: 10000, Currency: "USD", Reference: ref, Source: "right"},
				Transaction{ID: fmt.Sprintf("r%d-b", i), Date: date, Amount: 10000, Currency: "USD", Reference: ref, Source: "right"},
			)
		}
	}

	pair := config.Pair{
		AmountToleranceMinor: 0,
		Passes: []config.PassConfig{
			{Type: config.PassTypeReferenceOneToOne},
			{Type: config.PassTypeOneToMany},
		},
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := Reconcile("bench", "left", "right", left, right, pair)
		if err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(total), "left_rows/op")
	}
}
