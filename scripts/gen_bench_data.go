//go:build ignore

// gen_bench_data generates two synthetic CSV files for reconciliation benchmarking.
//
// Usage:
//
//	go run scripts/gen_bench_data.go [flags]
//
// Flags:
//
//	-rows int   Rows per file (default 20000000)
//	-out  dir   Output directory (default /tmp/bench-data)
//
// Output:
//
//	<out>/left.csv   — bank/ledger format:      date,description,amount,ref_id,currency
//	<out>/right.csv  — payment processor format: txn_date,txn_amount,txn_ref,merchant,txn_currency
//
// The two files intentionally use different column names to exercise the
// CSVParserCfg field mapping (date_col, amount_col, ref_col, name_col).
//
// Row distribution (default 20M rows per side):
//
//	85% (17M) matched:      same ref, same amount, same date on both sides
//	 5%  (1M) amount diff:  same ref + date; right amount is +1 to +50 minor units higher
//	 5%  (1M) timing diff:  same ref + amount; right date is +1 to +3 days later
//	 5%  (1M) left-only:    ref appears only in left.csv (unmatched left)
//	 5%  (1M) right-only:   ref appears only in right.csv (unmatched right)
//
// Amounts are written as integers (minor units). Use multiplier: 1 in the
// reconify config to consume these files without unit conversion.
//
// Date strings cycle over 730 unique values (2 calendar years). Because the
// parser's date cache holds up to 1000 unique keys, the cache stays fully warm
// for all 20M rows — matching real financial files where dates repeat daily.
//
// After generation the script prints the exact go test command to run the
// large-file benchmarks.
package main

import (
	"bufio"
	"flag"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const wbufSize = 4 << 20 // 4 MB write buffer

func main() {
	rows   := flag.Int("rows", 20_000_000, "rows per file")
	outDir := flag.String("out", "/tmp/bench-data", "output directory")
	flag.Parse()

	n         := *rows
	nMatch    := int(float64(n) * 0.85)
	nAmtDiff  := int(float64(n) * 0.05)
	nTimeDiff := int(float64(n) * 0.05)
	nUnmatchL := n - nMatch - nAmtDiff - nTimeDiff // remainder goes to unmatched-left
	nUnmatchR := int(float64(n) * 0.05)

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		log.Fatalf("mkdir %s: %v", *outDir, err)
	}

	// Precompute 730 date strings (2 calendar years: 2023-01-01 … 2024-12-31).
	// 730 < 1000 (the parser's date-cache cap), so the cache stays warm throughout.
	base  := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	dates := make([]string, 730)
	for d := range dates {
		dates[d] = base.AddDate(0, 0, d).Format("2006-01-02")
	}

	// Precompute 500 merchant name strings.
	merchants := make([]string, 500)
	for i := range merchants {
		merchants[i] = "Merchant " + strconv.Itoa(i)
	}

	leftPath  := filepath.Join(*outDir, "left.csv")
	rightPath := filepath.Join(*outDir, "right.csv")

	lf, err := os.Create(leftPath)
	if err != nil {
		log.Fatalf("create left.csv: %v", err)
	}
	rf, err := os.Create(rightPath)
	if err != nil {
		log.Fatalf("create right.csv: %v", err)
	}

	lw := bufio.NewWriterSize(lf, wbufSize)
	rw := bufio.NewWriterSize(rf, wbufSize)

	lw.WriteString("date,description,amount,ref_id,currency\n")
	rw.WriteString("txn_date,txn_amount,txn_ref,merchant,txn_currency\n")

	log.Printf("generating %d rows per side", n)
	log.Printf("  matched=%d  amount-diff=%d  timing-diff=%d  left-only=%d  right-only=%d",
		nMatch, nAmtDiff, nTimeDiff, nUnmatchL, nUnmatchR)

	start := time.Now()

	// Reusable row byte slices — backed arrays grow once and are reused every row,
	// avoiding per-row heap allocation in the generator itself.
	var lb, rb []byte

	// appendPaddedInt appends i zero-padded to exactly w decimal digits.
	appendPaddedInt := func(b []byte, i, w int) []byte {
		s := strconv.AppendInt(nil, int64(i), 10)
		for pad := w - len(s); pad > 0; pad-- {
			b = append(b, '0')
		}
		return append(b, s...)
	}

	// appendRef appends  prefix + "-" + 12-digit-zero-padded index.
	// Examples: appendRef(b, "TXN", 0) → "TXN-000000000000"
	//           appendRef(b, "ADIFF", 999999) → "ADIFF-000000999999"
	appendRef := func(b []byte, prefix string, i int) []byte {
		b = append(b, prefix...)
		b = append(b, '-')
		return appendPaddedInt(b, i, 12)
	}

	progress := func(done int) {
		if done%1_000_000 == 0 {
			log.Printf("  %3d M rows done  (%v elapsed)",
				done/1_000_000, time.Since(start).Round(time.Millisecond))
		}
	}

	// ── 1. Perfect matches ─────────────────────────────────────────────────
	// Both files get identical ref, amount, and date.
	for i := 0; i < nMatch; i++ {
		date   := dates[i%730]
		amount := int64(100 + i%999901)

		// left row:  date,description,amount,ref_id,currency
		lb = lb[:0]
		lb = append(lb, date...)
		lb = append(lb, ",Payment "...)
		lb = appendRef(lb, "TXN", i)
		lb = append(lb, ',')
		lb = strconv.AppendInt(lb, amount, 10)
		lb = append(lb, ',')
		lb = appendRef(lb, "TXN", i)
		lb = append(lb, ",USD\n"...)
		lw.Write(lb) //nolint:errcheck

		// right row: txn_date,txn_amount,txn_ref,merchant,txn_currency
		rb = rb[:0]
		rb = append(rb, date...)
		rb = append(rb, ',')
		rb = strconv.AppendInt(rb, amount, 10)
		rb = append(rb, ',')
		rb = appendRef(rb, "TXN", i)
		rb = append(rb, ',')
		rb = append(rb, merchants[i%500]...)
		rb = append(rb, ",USD\n"...)
		rw.Write(rb) //nolint:errcheck

		progress(i + 1)
	}

	// ── 2. Amount diffs ────────────────────────────────────────────────────
	// Same ref + date; right amount is leftAmt + (1..50).
	for i := 0; i < nAmtDiff; i++ {
		date     := dates[i%730]
		leftAmt  := int64(100 + i%999901)
		rightAmt := leftAmt + int64(i%50) + 1

		lb = lb[:0]
		lb = append(lb, date...)
		lb = append(lb, ",Payment "...)
		lb = appendRef(lb, "ADIFF", i)
		lb = append(lb, ',')
		lb = strconv.AppendInt(lb, leftAmt, 10)
		lb = append(lb, ',')
		lb = appendRef(lb, "ADIFF", i)
		lb = append(lb, ",USD\n"...)
		lw.Write(lb) //nolint:errcheck

		rb = rb[:0]
		rb = append(rb, date...)
		rb = append(rb, ',')
		rb = strconv.AppendInt(rb, rightAmt, 10)
		rb = append(rb, ',')
		rb = appendRef(rb, "ADIFF", i)
		rb = append(rb, ',')
		rb = append(rb, merchants[i%500]...)
		rb = append(rb, ",USD\n"...)
		rw.Write(rb) //nolint:errcheck

		progress(nMatch + i + 1)
	}

	// ── 3. Timing diffs ────────────────────────────────────────────────────
	// Same ref + amount; right date is +1 to +3 days after left date.
	// Index bounds: i%727 ∈ [0,726], i%3+1 ∈ [1,3] → max index 729 = len(dates)-1. ✓
	for i := 0; i < nTimeDiff; i++ {
		leftDate  := dates[i%727]
		rightDate := dates[i%727+i%3+1]
		amount    := int64(100 + i%999901)

		lb = lb[:0]
		lb = append(lb, leftDate...)
		lb = append(lb, ",Payment "...)
		lb = appendRef(lb, "TDIFF", i)
		lb = append(lb, ',')
		lb = strconv.AppendInt(lb, amount, 10)
		lb = append(lb, ',')
		lb = appendRef(lb, "TDIFF", i)
		lb = append(lb, ",USD\n"...)
		lw.Write(lb) //nolint:errcheck

		rb = rb[:0]
		rb = append(rb, rightDate...)
		rb = append(rb, ',')
		rb = strconv.AppendInt(rb, amount, 10)
		rb = append(rb, ',')
		rb = appendRef(rb, "TDIFF", i)
		rb = append(rb, ',')
		rb = append(rb, merchants[i%500]...)
		rb = append(rb, ",USD\n"...)
		rw.Write(rb) //nolint:errcheck

		progress(nMatch + nAmtDiff + i + 1)
	}

	// ── 4. Unmatched left ──────────────────────────────────────────────────
	// LONLY-* refs appear only in left.csv.
	for i := 0; i < nUnmatchL; i++ {
		date   := dates[i%730]
		amount := int64(100 + i%999901)

		lb = lb[:0]
		lb = append(lb, date...)
		lb = append(lb, ",Payment "...)
		lb = appendRef(lb, "LONLY", i)
		lb = append(lb, ',')
		lb = strconv.AppendInt(lb, amount, 10)
		lb = append(lb, ',')
		lb = appendRef(lb, "LONLY", i)
		lb = append(lb, ",USD\n"...)
		lw.Write(lb) //nolint:errcheck

		progress(nMatch + nAmtDiff + nTimeDiff + i + 1)
	}

	// ── 5. Unmatched right ─────────────────────────────────────────────────
	// RONLY-* refs appear only in right.csv.
	for i := 0; i < nUnmatchR; i++ {
		date   := dates[i%730]
		amount := int64(100 + i%999901)

		rb = rb[:0]
		rb = append(rb, date...)
		rb = append(rb, ',')
		rb = strconv.AppendInt(rb, amount, 10)
		rb = append(rb, ',')
		rb = appendRef(rb, "RONLY", i)
		rb = append(rb, ',')
		rb = append(rb, merchants[i%500]...)
		rb = append(rb, ",USD\n"...)
		rw.Write(rb) //nolint:errcheck
	}

	// Flush and close — explicit ordering: flush writer, then close file handle.
	if err := lw.Flush(); err != nil {
		log.Fatalf("flush left.csv: %v", err)
	}
	if err := lf.Close(); err != nil {
		log.Fatalf("close left.csv: %v", err)
	}
	if err := rw.Flush(); err != nil {
		log.Fatalf("flush right.csv: %v", err)
	}
	if err := rf.Close(); err != nil {
		log.Fatalf("close right.csv: %v", err)
	}

	elapsed := time.Since(start)
	leftInfo, _  := os.Stat(leftPath)
	rightInfo, _ := os.Stat(rightPath)

	log.Printf("done in %v", elapsed.Round(time.Millisecond))
	log.Printf("left.csv:  %4d MB  (%s)", leftInfo.Size()/1024/1024, leftPath)
	log.Printf("right.csv: %4d MB  (%s)", rightInfo.Size()/1024/1024, rightPath)
	log.Printf("")
	log.Printf("next step — run large-file benchmarks:")
	log.Printf("  BENCH_DATA_DIR=%s go test -run='^$' -bench=BenchmarkReconcileStreaming_20M \\", *outDir)
	log.Printf("    -benchtime=1x -benchmem -timeout=600s ./engine/...")
	log.Printf("")
	log.Printf("or use the full benchmark suite:")
	log.Printf("  ./scripts/bench_full.sh --data-dir %s", *outDir)
}
