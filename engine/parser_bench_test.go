package engine

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"testing"

	"github.com/reconify/reconify/config"
)

// writeSyntheticCSV writes n rows to a temp file and returns the path.
// Dates repeat on a 365-day cycle to simulate real financial files.
// References are unique per row.
func writeSyntheticCSV(tb testing.TB, rows int) string {
	tb.Helper()
	f, err := os.CreateTemp(tb.TempDir(), "bench_*.csv")
	if err != nil {
		tb.Fatal(err)
	}
	defer f.Close()

	fmt.Fprintln(f, "date,amount,currency,reference,name")
	dates := [...]string{
		"2024-01-01", "2024-01-02", "2024-01-03", "2024-01-04", "2024-01-05",
		"2024-02-01", "2024-02-02", "2024-02-03", "2024-02-04", "2024-02-05",
	}
	for i := 0; i < rows; i++ {
		fmt.Fprintf(f, "%s,%d.%02d,USD,REF%010d,Merchant %d\n",
			dates[i%len(dates)], 100+i%10000, i%100, i, i%500)
	}
	return f.Name()
}

func benchmarkParseCSVEach(b *testing.B, rows int, skipRaw bool) {
	path := writeSyntheticCSV(b, rows)
	cfg := config.CSVParserCfg{
		Type:       "csv",
		DateCol:    "date",
		DateLayout: "2006-01-02",
		AmountCol:  "amount",
		Multiplier: 100,
		CurrencyCol: "currency",
		RefCol:     "reference",
		NameCol:    "name",
		SkipRaw:    skipRaw,
	}
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		count := 0
		if err := ParseCSVEach(context.Background(), "bench", path, cfg, func(_ Transaction, _ int) error {
			count++
			return nil
		}); err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(count), "rows/op")
	}
}

func BenchmarkParseCSVEach_100k(b *testing.B)         { benchmarkParseCSVEach(b, 100_000, false) }
func BenchmarkParseCSVEach_1M(b *testing.B)           { benchmarkParseCSVEach(b, 1_000_000, false) }
func BenchmarkParseCSVEach_1M_SkipRaw(b *testing.B)   { benchmarkParseCSVEach(b, 1_000_000, true) }

// TestMemoryIndex_PerRowFootprint measures the actual heap bytes consumed per
// indexed row. This replaces estimation with measurement.
// Run with: go test -run TestMemoryIndex_PerRowFootprint -v ./engine/...
func TestMemoryIndex_PerRowFootprint(t *testing.T) {
	const rows = 1_000_000

	path := writeSyntheticCSV(t, rows)
	cfg := config.CSVParserCfg{
		Type:       "csv",
		DateCol:    "date",
		DateLayout: "2006-01-02",
		AmountCol:  "amount",
		Multiplier: 100,
		CurrencyCol: "currency",
		RefCol:     "reference",
		NameCol:    "name",
		SkipRaw:    true,
	}

	idx := NewMemoryIndex()

	// Force GC before measurement
	runtime.GC()
	var msBefore runtime.MemStats
	runtime.ReadMemStats(&msBefore)

	if err := ParseCSVEach(context.Background(), "bench", path, cfg, func(tx Transaction, _ int) error {
		return idx.Add(tx)
	}); err != nil {
		t.Fatal(err)
	}

	runtime.GC()
	var msAfter runtime.MemStats
	runtime.ReadMemStats(&msAfter)

	// TotalAlloc is monotonically increasing — unaffected by GC cycles.
	// It measures total bytes allocated during the index build, approximating
	// the per-row allocation cost (including short-lived temporaries).
	allocDelta := int64(msAfter.TotalAlloc) - int64(msBefore.TotalAlloc)
	perRow := allocDelta / rows

	t.Logf("memoryIndex: %d rows, total alloc %d MB, ~%d bytes/row (includes GC'd temporaries)",
		rows, allocDelta/1024/1024, perRow)

	idx.Close()
}
