//go:build ignore

// gen_bench_data generates realistic synthetic finance CSV files for
// reconciliation benchmarking and parser hardening.
//
// Usage:
//
//	go run scripts/gen_bench_data.go [flags]
//
// Flags:
//
//	-rows int              Rows per file for main benchmark pair (default 20000000)
//	-out dir               Output directory (default /tmp/bench-data)
//	-dirty-rows int        Rows for each dirty/error fixture file (default 5000)
//	-with-error-cases bool Generate dirty/error fixture files (default true)
//
// Main output (wide schemas, 10+ columns):
//
//	<out>/left.csv
//	<out>/right.csv
//
// Dirty fixture output (small files with format/data issues):
//
//	<out>/error-cases/valid_wide.csv
//	<out>/error-cases/bad_date.csv
//	<out>/error-cases/empty_date.csv
//	<out>/error-cases/bad_amount.csv
//	<out>/error-cases/empty_amount.csv
//	<out>/error-cases/malformed_csv.csv
//
// Row distribution for the main pair (default 20M rows each side):
//
//	85% matched
//	 5% amount diff
//	 5% timing diff
//	 5% left-only
//	 5% right-only
package main

import (
	"bufio"
	"encoding/csv"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const wbufSize = 4 << 20 // 4 MB write buffer

var leftHeader = []string{
	"date", "description", "amount", "ref_id", "currency",
	"account_id", "customer_id", "branch_code", "txn_type", "status",
	"channel", "country", "batch_id", "line_no", "value_date",
	"booking_ts", "balance_after_minor", "processor_hint",
}

var rightHeader = []string{
	"txn_date", "txn_amount", "txn_ref", "merchant", "txn_currency",
	"merchant_id", "settlement_batch", "payment_method", "network", "fee_minor",
	"tax_minor", "country_code", "auth_code", "status", "captured_at",
	"settled_at", "payout_account", "processor_note",
}

type profile struct {
	dates      []string
	valueDates []string
	bookingTS  []string
	capturedTS []string
	settledTS  []string

	merchants []string
	channels  []string
	countries []string
	branches  []string

	leftStatus  []string
	rightStatus []string
	txnTypes    []string
	methods     []string
	networks    []string
}

func main() {
	rows := flag.Int("rows", 20_000_000, "rows per file")
	outDir := flag.String("out", "/tmp/bench-data", "output directory")
	dirtyRows := flag.Int("dirty-rows", 5_000, "rows per dirty/error fixture file")
	withErrorCases := flag.Bool("with-error-cases", true, "generate dirty/error fixture files")
	flag.Parse()

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		log.Fatalf("mkdir %s: %v", *outDir, err)
	}

	p := buildProfile()

	if err := generateMainPair(*rows, *outDir, p); err != nil {
		log.Fatalf("generate main pair: %v", err)
	}

	if *withErrorCases {
		if err := generateErrorCases(*outDir, *dirtyRows, p); err != nil {
			log.Fatalf("generate error cases: %v", err)
		}
	}

	outAbs, _ := filepath.Abs(*outDir)
	log.Printf("")
	log.Printf("next step — run large-file benchmarks:")
	log.Printf("  BENCH_DATA_DIR=%s go test -run='^$' -bench='20M' -benchtime=1x -benchmem ./engine/...", outAbs)
	log.Printf("")
	log.Printf("or use full suite:")
	log.Printf("  ./scripts/bench_full.sh --data-dir %s", outAbs)
}

func buildProfile() profile {
	base := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	dates := make([]string, 730)
	valueDates := make([]string, 730)
	bookingTS := make([]string, 730)
	capturedTS := make([]string, 730)
	settledTS := make([]string, 730)

	for i := range dates {
		d := base.AddDate(0, 0, i)
		dates[i] = d.Format("2006-01-02")

		vd := d.AddDate(0, 0, -1)
		valueDates[i] = vd.Format("2006-01-02")
		bookingTS[i] = d.Format("2006-01-02") + "T09:31:00Z"
		capturedTS[i] = d.Format("2006-01-02") + "T10:05:00Z"
		settledTS[i] = d.Add(24*time.Hour).Format("2006-01-02") + "T02:00:00Z"
	}

	merchants := make([]string, 500)
	for i := range merchants {
		merchants[i] = "Merchant-" + strconv.Itoa(i)
	}

	return profile{
		dates:      dates,
		valueDates: valueDates,
		bookingTS:  bookingTS,
		capturedTS: capturedTS,
		settledTS:  settledTS,
		merchants:  merchants,
		channels:   []string{"web", "mobile", "pos", "api", "batch", "ivr"},
		countries:  []string{"US", "GB", "DE", "NG", "IN", "SG", "KE", "FR"},
		branches:   []string{"NYC01", "LON02", "BER03", "LOS04", "MUM05", "SIN06"},
		leftStatus: []string{"posted", "cleared", "pending"},
		rightStatus: []string{
			"captured", "settled", "pending_review", "reversed",
		},
		txnTypes: []string{"debit", "credit"},
		methods:  []string{"card", "bank_transfer", "wallet", "direct_debit"},
		networks: []string{"visa", "mastercard", "ach", "sepa", "nibss"},
	}
}

func generateMainPair(n int, outDir string, p profile) error {
	nMatch := int(float64(n) * 0.85)
	nAmtDiff := int(float64(n) * 0.05)
	nTimeDiff := int(float64(n) * 0.05)
	nUnmatchL := n - nMatch - nAmtDiff - nTimeDiff
	nUnmatchR := int(float64(n) * 0.05)

	leftPath := filepath.Join(outDir, "left.csv")
	rightPath := filepath.Join(outDir, "right.csv")

	lf, err := os.Create(leftPath)
	if err != nil {
		return fmt.Errorf("create left.csv: %w", err)
	}
	defer lf.Close()
	rf, err := os.Create(rightPath)
	if err != nil {
		return fmt.Errorf("create right.csv: %w", err)
	}
	defer rf.Close()

	lw := bufio.NewWriterSize(lf, wbufSize)
	rw := bufio.NewWriterSize(rf, wbufSize)

	if _, err := lw.WriteString(strings.Join(leftHeader, ",") + "\n"); err != nil {
		return err
	}
	if _, err := rw.WriteString(strings.Join(rightHeader, ",") + "\n"); err != nil {
		return err
	}

	log.Printf("generating %d rows per side (wide schema)", n)
	log.Printf("  matched=%d  amount-diff=%d  timing-diff=%d  left-only=%d  right-only=%d",
		nMatch, nAmtDiff, nTimeDiff, nUnmatchL, nUnmatchR)

	start := time.Now()
	var lb, rb []byte

	appendPaddedInt := func(b []byte, i, w int) []byte {
		s := strconv.AppendInt(nil, int64(i), 10)
		for pad := w - len(s); pad > 0; pad-- {
			b = append(b, '0')
		}
		return append(b, s...)
	}
	appendRef := func(b []byte, prefix string, i int) []byte {
		b = append(b, prefix...)
		b = append(b, '-')
		return appendPaddedInt(b, i, 12)
	}
	buildRef := func(prefix string, i int) string {
		buf := make([]byte, 0, len(prefix)+13)
		buf = appendRef(buf, prefix, i)
		return string(buf)
	}

	progress := func(done int) {
		if done%1_000_000 == 0 {
			log.Printf("  %3d M rows done  (%v elapsed)", done/1_000_000, time.Since(start).Round(time.Millisecond))
		}
	}

	leftRow := func(date, valueDate, bookingTS string, amount int64, ref string, i int) []byte {
		lb = lb[:0]
		feeHint := int64((i % 30) + 1)
		balanceAfter := int64(5_000_000+(i%500_000)) + amount - feeHint

		lb = append(lb, date...)
		lb = append(lb, ",Payment-"...)
		lb = append(lb, ref...)
		lb = append(lb, ',')
		lb = strconv.AppendInt(lb, amount, 10)
		lb = append(lb, ',')
		lb = append(lb, ref...)
		lb = append(lb, ",USD,ACCT-"...)
		lb = appendPaddedInt(lb, i%250_000, 6)
		lb = append(lb, ",CUST-"...)
		lb = appendPaddedInt(lb, i%900_000, 6)
		lb = append(lb, ',')
		lb = append(lb, p.branches[i%len(p.branches)]...)
		lb = append(lb, ',')
		lb = append(lb, p.txnTypes[i%len(p.txnTypes)]...)
		lb = append(lb, ',')
		lb = append(lb, p.leftStatus[i%len(p.leftStatus)]...)
		lb = append(lb, ',')
		lb = append(lb, p.channels[i%len(p.channels)]...)
		lb = append(lb, ',')
		lb = append(lb, p.countries[i%len(p.countries)]...)
		lb = append(lb, ",BATCH-2024-"...)
		lb = appendPaddedInt(lb, i%365, 3)
		lb = append(lb, ',')
		lb = strconv.AppendInt(lb, int64(i+1), 10)
		lb = append(lb, ',')
		lb = append(lb, valueDate...)
		lb = append(lb, ',')
		lb = append(lb, bookingTS...)
		lb = append(lb, ',')
		lb = strconv.AppendInt(lb, balanceAfter, 10)
		lb = append(lb, ",feehint-"...)
		lb = strconv.AppendInt(lb, feeHint, 10)
		lb = append(lb, '\n')
		return lb
	}

	rightRow := func(date, capturedTS, settledTS string, amount int64, ref string, i int) []byte {
		rb = rb[:0]
		fee := int64((i % 30) + 1)
		tax := fee / 10

		rb = append(rb, date...)
		rb = append(rb, ',')
		rb = strconv.AppendInt(rb, amount, 10)
		rb = append(rb, ',')
		rb = append(rb, ref...)
		rb = append(rb, ',')
		rb = append(rb, p.merchants[i%len(p.merchants)]...)
		rb = append(rb, ",USD,MER-"...)
		rb = appendPaddedInt(rb, i%800_000, 6)
		rb = append(rb, ",SETTLE-2024-"...)
		rb = appendPaddedInt(rb, i%365, 3)
		rb = append(rb, ',')
		rb = append(rb, p.methods[i%len(p.methods)]...)
		rb = append(rb, ',')
		rb = append(rb, p.networks[i%len(p.networks)]...)
		rb = append(rb, ',')
		rb = strconv.AppendInt(rb, fee, 10)
		rb = append(rb, ',')
		rb = strconv.AppendInt(rb, tax, 10)
		rb = append(rb, ',')
		rb = append(rb, p.countries[(i+2)%len(p.countries)]...)
		rb = append(rb, ",AUTH"...)
		rb = appendPaddedInt(rb, i%9_999_999, 7)
		rb = append(rb, ',')
		rb = append(rb, p.rightStatus[i%len(p.rightStatus)]...)
		rb = append(rb, ',')
		rb = append(rb, capturedTS...)
		rb = append(rb, ',')
		rb = append(rb, settledTS...)
		rb = append(rb, ",PAYOUT-"...)
		rb = appendPaddedInt(rb, i%5_000, 4)
		rb = append(rb, ",note-"...)
		rb = append(rb, p.channels[i%len(p.channels)]...)
		rb = append(rb, '\n')
		return rb
	}

	// 1) Perfect matches
	for i := 0; i < nMatch; i++ {
		idx := i % len(p.dates)
		date := p.dates[idx]
		amount := int64(100 + i%999_901)
		ref := buildRef("TXN", i)

		if _, err := lw.Write(leftRow(date, p.valueDates[idx], p.bookingTS[idx], amount, ref, i)); err != nil {
			return err
		}
		if _, err := rw.Write(rightRow(date, p.capturedTS[idx], p.settledTS[idx], amount, ref, i)); err != nil {
			return err
		}
		progress(i + 1)
	}

	// 2) Amount diffs
	for i := 0; i < nAmtDiff; i++ {
		idx := i % len(p.dates)
		date := p.dates[idx]
		leftAmt := int64(100 + i%999_901)
		rightAmt := leftAmt + int64(i%50) + 1
		ref := buildRef("ADIFF", i)

		if _, err := lw.Write(leftRow(date, p.valueDates[idx], p.bookingTS[idx], leftAmt, ref, i)); err != nil {
			return err
		}
		if _, err := rw.Write(rightRow(date, p.capturedTS[idx], p.settledTS[idx], rightAmt, ref, i)); err != nil {
			return err
		}
		progress(nMatch + i + 1)
	}

	// 3) Timing diffs
	for i := 0; i < nTimeDiff; i++ {
		leftIdx := i % 727
		rightIdx := leftIdx + (i%3 + 1)
		amount := int64(100 + i%999_901)
		ref := buildRef("TDIFF", i)

		if _, err := lw.Write(leftRow(p.dates[leftIdx], p.valueDates[leftIdx], p.bookingTS[leftIdx], amount, ref, i)); err != nil {
			return err
		}
		if _, err := rw.Write(rightRow(p.dates[rightIdx], p.capturedTS[rightIdx], p.settledTS[rightIdx], amount, ref, i)); err != nil {
			return err
		}
		progress(nMatch + nAmtDiff + i + 1)
	}

	// 4) Unmatched left
	for i := 0; i < nUnmatchL; i++ {
		idx := i % len(p.dates)
		amount := int64(100 + i%999_901)
		ref := buildRef("LONLY", i)
		if _, err := lw.Write(leftRow(p.dates[idx], p.valueDates[idx], p.bookingTS[idx], amount, ref, i)); err != nil {
			return err
		}
		progress(nMatch + nAmtDiff + nTimeDiff + i + 1)
	}

	// 5) Unmatched right
	for i := 0; i < nUnmatchR; i++ {
		idx := i % len(p.dates)
		amount := int64(100 + i%999_901)
		ref := buildRef("RONLY", i)
		if _, err := rw.Write(rightRow(p.dates[idx], p.capturedTS[idx], p.settledTS[idx], amount, ref, i)); err != nil {
			return err
		}
	}

	if err := lw.Flush(); err != nil {
		return fmt.Errorf("flush left.csv: %w", err)
	}
	if err := rw.Flush(); err != nil {
		return fmt.Errorf("flush right.csv: %w", err)
	}

	leftInfo, _ := os.Stat(leftPath)
	rightInfo, _ := os.Stat(rightPath)
	elapsed := time.Since(start)

	log.Printf("done in %v", elapsed.Round(time.Millisecond))
	log.Printf("left.csv:  %4d MB  (%s)", leftInfo.Size()/1024/1024, leftPath)
	log.Printf("right.csv: %4d MB  (%s)", rightInfo.Size()/1024/1024, rightPath)
	return nil
}

func generateErrorCases(outDir string, rows int, p profile) error {
	if rows < 10 {
		rows = 10
	}
	casesDir := filepath.Join(outDir, "error-cases")
	if err := os.MkdirAll(casesDir, 0o755); err != nil {
		return err
	}

	writeCase := func(path string, mutate func(i int, row []string)) error {
		f, err := os.Create(path)
		if err != nil {
			return err
		}
		defer f.Close()
		w := csv.NewWriter(f)
		if err := w.Write(leftHeader); err != nil {
			return err
		}
		for i := 0; i < rows; i++ {
			idx := i % len(p.dates)
			row := []string{
				p.dates[idx],
				fmt.Sprintf("Invoice %d, Retail Segment", i),
				"1,234.56",
				fmt.Sprintf("ERR-%06d", i),
				"USD",
				fmt.Sprintf("ACCT-%06d", i%250000),
				fmt.Sprintf("CUST-%06d", i%900000),
				p.branches[i%len(p.branches)],
				p.txnTypes[i%len(p.txnTypes)],
				p.leftStatus[i%len(p.leftStatus)],
				p.channels[i%len(p.channels)],
				p.countries[i%len(p.countries)],
				fmt.Sprintf("BATCH-2024-%03d", i%365),
				strconv.Itoa(i + 1),
				p.valueDates[idx],
				p.bookingTS[idx],
				"5000000",
				"fixture",
			}
			mutate(i, row)
			if err := w.Write(row); err != nil {
				return err
			}
		}
		w.Flush()
		return w.Error()
	}

	if err := writeCase(filepath.Join(casesDir, "valid_wide.csv"), func(_ int, _ []string) {}); err != nil {
		return err
	}
	if err := writeCase(filepath.Join(casesDir, "bad_date.csv"), func(i int, row []string) {
		if i == rows/2 {
			row[0] = "2024-99-99"
		}
	}); err != nil {
		return err
	}
	if err := writeCase(filepath.Join(casesDir, "empty_date.csv"), func(i int, row []string) {
		if i == rows/2 {
			row[0] = ""
		}
	}); err != nil {
		return err
	}
	if err := writeCase(filepath.Join(casesDir, "bad_amount.csv"), func(i int, row []string) {
		if i == rows/2 {
			row[2] = "12O0.50"
		}
	}); err != nil {
		return err
	}
	if err := writeCase(filepath.Join(casesDir, "empty_amount.csv"), func(i int, row []string) {
		if i == rows/2 {
			row[2] = ""
		}
	}); err != nil {
		return err
	}

	malformedPath := filepath.Join(casesDir, "malformed_csv.csv")
	var malformed strings.Builder
	malformed.WriteString(strings.Join(leftHeader, ",") + "\n")
	malformed.WriteString("2024-01-01,\"Invoice 1, Retail\",1234.56,REF-1,USD,ACCT-1,CUST-1,NYC01,debit,posted,api,US,BATCH-001,1,2023-12-31,2024-01-01T10:00:00Z,5000000,fixture\n")
	// Unclosed quote in description to force CSV parse error.
	malformed.WriteString("2024-01-02,\"Broken description,1234.56,REF-2,USD,ACCT-2,CUST-2,NYC01,debit,posted,api,US,BATCH-001,2,2024-01-01,2024-01-02T10:00:00Z,5000001,fixture\n")
	if err := os.WriteFile(malformedPath, []byte(malformed.String()), 0o644); err != nil {
		return err
	}

	readme := filepath.Join(casesDir, "README.md")
	readmeContent := strings.Join([]string{
		"# Error Case Fixtures",
		"",
		"These files emulate real finance CSV issues for parser hardening.",
		"",
		"- `valid_wide.csv`: valid wide schema with quoted comma in description and 18 columns.",
		"- `bad_date.csv`: one row contains invalid date `2024-99-99`.",
		"- `empty_date.csv`: one row contains empty required date field.",
		"- `bad_amount.csv`: one row contains invalid amount `12O0.50`.",
		"- `empty_amount.csv`: one row contains empty required amount field.",
		"- `malformed_csv.csv`: one row has unclosed quote to trigger CSV parse error.",
		"",
		"Recommended parser config for `valid_wide.csv`:",
		"- `date_col: date`",
		"- `date_layout: 2006-01-02`",
		"- `amount_col: amount`",
		"- `decimal: .`",
		"- `thousands: ,`",
		"- `multiplier: 100`",
		"- `currency_col: currency`",
		"- `ref_col: ref_id`",
		"- `name_col: description`",
	}, "\n") + "\n"
	if err := os.WriteFile(readme, []byte(readmeContent), 0o644); err != nil {
		return err
	}

	log.Printf("generated dirty/error fixtures in %s", casesDir)
	return nil
}
