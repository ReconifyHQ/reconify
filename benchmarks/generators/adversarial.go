//go:build ignore

// adversarial generates deterministic adversarial benchmark fixtures for Reconify.
//
// Each scenario produces left.csv, right.csv, and manifest.json in the given
// output directory. The manifest stores expected classification counts and
// per-currency monetary totals so the verifier can assert correctness without
// re-running generation.
//
// Usage:
//
//	go run benchmarks/generators/adversarial.go [flags]
//
// Flags:
//
//	--scenario string     Scenario name (required). One of:
//	                        uniform_unique_refs, hot_skewed_refs,
//	                        high_duplicate_pressure, high_result_emission,
//	                        one_to_many_settlement, many_to_many_settlement
//	--rows int            Left-side row count (default 100000)
//	--output-dir string   Output directory (default /tmp/adversarial/<scenario>)
//	--seed int            seed for deterministic fixture ordering and values (default 42)
//
// All six scenarios use a single currency (USD) per run. Mixed-currency totals
// are rejected by Reconify; run each currency in a separate invocation.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const wbufSize = 4 << 20 // 4 MB write buffer

var generationSeed int64

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

// adversarialManifest is written alongside the fixtures.
// Verifiers byte-compare manifests across runs with the same seed to confirm
// reproducibility. Only timing, RSS, GC, and disk metrics are non-reproducible.
type adversarialManifest struct {
	Scenario       string                    `json:"scenario"`
	Seed           int64                     `json:"seed"`
	Rows           int                       `json:"rows"`
	PassType       string                    `json:"pass_type"`
	Summary        manifestSummary           `json:"summary"`
	CurrencyTotals map[string]currencyTotals `json:"currency_totals"`
	SkippedMatrix  []skippedEntry            `json:"skipped_matrix,omitempty"`
}

type manifestSummary struct {
	TotalLeft              int `json:"total_left"`
	TotalRight             int `json:"total_right"`
	Matched                int `json:"matched"`
	UnmatchedLeft          int `json:"unmatched_left"`
	UnmatchedRight         int `json:"unmatched_right"`
	AmountDiffCount        int `json:"amount_diff_count"`
	TimingDiffCount        int `json:"timing_diff_count"`
	DuplicateCount         int `json:"duplicate_count"`
	GroupedMatchedCount    int `json:"grouped_matched_count,omitempty"`
	ManyToManyMatchedCount int `json:"many_to_many_matched_count,omitempty"`
}

type currencyTotals struct {
	MatchedAmountLeft    int64 `json:"matched_amount_left"`
	MatchedAmountRight   int64 `json:"matched_amount_right"`
	UnmatchedAmountLeft  int64 `json:"unmatched_amount_left"`
	UnmatchedAmountRight int64 `json:"unmatched_amount_right"`
}

type skippedEntry struct {
	Backend string `json:"backend,omitempty"`
	Format  string `json:"format,omitempty"`
	Reason  string `json:"reason"`
}

// genProfile holds pre-computed date/time/name pools shared across scenario generators.
type genProfile struct {
	dates      []string
	valueDates []string
	bookingTS  []string
	capturedTS []string
	settledTS  []string
	merchants  []string
	channels   []string
	countries  []string
	branches   []string
	txnTypes   []string
	methods    []string
	networks   []string
}

func buildProfile() genProfile {
	base := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	const days = 730
	dates := make([]string, days)
	valueDates := make([]string, days)
	bookingTS := make([]string, days)
	capturedTS := make([]string, days)
	settledTS := make([]string, days)
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
	return genProfile{
		dates:      dates,
		valueDates: valueDates,
		bookingTS:  bookingTS,
		capturedTS: capturedTS,
		settledTS:  settledTS,
		merchants:  merchants,
		channels:   []string{"web", "mobile", "pos", "api", "batch", "ivr"},
		countries:  []string{"US", "GB", "DE", "NG", "IN", "SG", "KE", "FR"},
		branches:   []string{"NYC01", "LON02", "BER03", "LOS04", "MUM05", "SIN06"},
		txnTypes:   []string{"debit", "credit"},
		methods:    []string{"card", "bank_transfer", "wallet", "direct_debit"},
		networks:   []string{"visa", "mastercard", "ach", "sepa", "nibss"},
	}
}

// rowBuilder assembles CSV rows into a reusable byte buffer.
type rowBuilder struct {
	buf     []byte
	profile *genProfile
}

func newRowBuilder(p *genProfile) *rowBuilder { return &rowBuilder{profile: p} }

func (rb *rowBuilder) appendPaddedInt(n, width int) {
	s := strconv.Itoa(n)
	for pad := width - len(s); pad > 0; pad-- {
		rb.buf = append(rb.buf, '0')
	}
	rb.buf = append(rb.buf, s...)
}

// leftRow builds a left CSV data row (no trailing newline).
func (rb *rowBuilder) leftRow(ref string, amount int64, dateIdx, lineNo int, processorHint string) []byte {
	rb.buf = rb.buf[:0]
	p := rb.profile
	i := lineNo
	rb.buf = append(rb.buf, p.dates[dateIdx%len(p.dates)]...)
	rb.buf = append(rb.buf, ",Payment-"...)
	rb.buf = append(rb.buf, ref...)
	rb.buf = append(rb.buf, ',')
	rb.buf = strconv.AppendInt(rb.buf, amount, 10)
	rb.buf = append(rb.buf, ',')
	rb.buf = append(rb.buf, ref...)
	rb.buf = append(rb.buf, ",USD,ACCT-"...)
	rb.appendPaddedInt(i%250000, 6)
	rb.buf = append(rb.buf, ",CUST-"...)
	rb.appendPaddedInt(i%900000, 6)
	rb.buf = append(rb.buf, ',')
	rb.buf = append(rb.buf, p.branches[i%len(p.branches)]...)
	rb.buf = append(rb.buf, ',')
	rb.buf = append(rb.buf, p.txnTypes[i%len(p.txnTypes)]...)
	rb.buf = append(rb.buf, ",posted,"...)
	rb.buf = append(rb.buf, p.channels[i%len(p.channels)]...)
	rb.buf = append(rb.buf, ',')
	rb.buf = append(rb.buf, p.countries[i%len(p.countries)]...)
	rb.buf = append(rb.buf, ",BATCH-2024-"...)
	rb.appendPaddedInt(i%365, 3)
	rb.buf = append(rb.buf, ',')
	rb.buf = strconv.AppendInt(rb.buf, int64(lineNo+1), 10)
	rb.buf = append(rb.buf, ',')
	rb.buf = append(rb.buf, p.valueDates[dateIdx%len(p.valueDates)]...)
	rb.buf = append(rb.buf, ',')
	rb.buf = append(rb.buf, p.bookingTS[dateIdx%len(p.bookingTS)]...)
	rb.buf = append(rb.buf, ',')
	rb.buf = strconv.AppendInt(rb.buf, 5000000+amount, 10)
	rb.buf = append(rb.buf, ',')
	rb.buf = append(rb.buf, processorHint...)
	rb.buf = append(rb.buf, '\n')
	return rb.buf
}

// rightRow builds a right CSV data row.
func (rb *rowBuilder) rightRow(ref string, amount int64, dateIdx, lineNo int) []byte {
	rb.buf = rb.buf[:0]
	p := rb.profile
	i := lineNo
	fee := int64((i % 30) + 1)
	tax := fee / 10
	rb.buf = append(rb.buf, p.dates[dateIdx%len(p.dates)]...)
	rb.buf = append(rb.buf, ',')
	rb.buf = strconv.AppendInt(rb.buf, amount, 10)
	rb.buf = append(rb.buf, ',')
	rb.buf = append(rb.buf, ref...)
	rb.buf = append(rb.buf, ',')
	rb.buf = append(rb.buf, p.merchants[i%len(p.merchants)]...)
	rb.buf = append(rb.buf, ",USD,MER-"...)
	rb.appendPaddedInt(i%800000, 6)
	rb.buf = append(rb.buf, ",SETTLE-2024-"...)
	rb.appendPaddedInt(i%365, 3)
	rb.buf = append(rb.buf, ',')
	rb.buf = append(rb.buf, p.methods[i%len(p.methods)]...)
	rb.buf = append(rb.buf, ',')
	rb.buf = append(rb.buf, p.networks[i%len(p.networks)]...)
	rb.buf = append(rb.buf, ',')
	rb.buf = strconv.AppendInt(rb.buf, fee, 10)
	rb.buf = append(rb.buf, ',')
	rb.buf = strconv.AppendInt(rb.buf, tax, 10)
	rb.buf = append(rb.buf, ',')
	rb.buf = append(rb.buf, p.countries[(i+2)%len(p.countries)]...)
	rb.buf = append(rb.buf, ",AUTH"...)
	rb.appendPaddedInt(i%9999999, 7)
	rb.buf = append(rb.buf, ",captured,"...)
	rb.buf = append(rb.buf, p.capturedTS[dateIdx%len(p.capturedTS)]...)
	rb.buf = append(rb.buf, ',')
	rb.buf = append(rb.buf, p.settledTS[dateIdx%len(p.settledTS)]...)
	rb.buf = append(rb.buf, ",PAYOUT-"...)
	rb.appendPaddedInt(i%5000, 4)
	rb.buf = append(rb.buf, ",note-"...)
	rb.buf = append(rb.buf, p.channels[i%len(p.channels)]...)
	rb.buf = append(rb.buf, '\n')
	return rb.buf
}

func rowAmount(i int) int64 {
	offset := generationSeed % 99000
	if offset < 0 {
		offset += 99000
	}
	return int64(1000 + (i+int(offset))%99000)
}

func fixtureOrder(n int, stream int64) []int {
	seed := generationSeed ^ (stream * 6364136223846793005)
	return rand.New(rand.NewSource(seed)).Perm(n)
}

// openCSVPair opens left.csv and right.csv for writing with buffered I/O.
func openCSVPair(outDir string) (*bufio.Writer, *bufio.Writer, func(), error) {
	lf, err := os.Create(filepath.Join(outDir, "left.csv"))
	if err != nil {
		return nil, nil, nil, err
	}
	rf, err := os.Create(filepath.Join(outDir, "right.csv"))
	if err != nil {
		lf.Close()
		return nil, nil, nil, err
	}
	lw := bufio.NewWriterSize(lf, wbufSize)
	rw := bufio.NewWriterSize(rf, wbufSize)
	cleanup := func() { lf.Close(); rf.Close() }
	return lw, rw, cleanup, nil
}

func writeHeader(w *bufio.Writer, cols []string) error {
	_, err := w.WriteString(strings.Join(cols, ",") + "\n")
	return err
}

func flushAndClose(lw, rw *bufio.Writer) error {
	if err := lw.Flush(); err != nil {
		return fmt.Errorf("flush left.csv: %w", err)
	}
	if err := rw.Flush(); err != nil {
		return fmt.Errorf("flush right.csv: %w", err)
	}
	return nil
}

// writeManifest encodes the manifest as pretty-printed JSON.
func writeManifest(outDir string, m adversarialManifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(outDir, "manifest.json"), append(data, '\n'), 0o644)
}

// ---------------------------------------------------------------------------
// Scenario: uniform_unique_refs
//
// All references unique. Baseline throughput stress with a standard
// 85/5/5/5 left distribution and 5% right-only rows.
// ---------------------------------------------------------------------------

func generateUniformUniqueRefs(rows int, outDir string) (adversarialManifest, error) {
	nMatch := rows * 85 / 100
	nAmtDiff := rows * 5 / 100
	nTimeDiff := rows * 5 / 100
	nLeftOnly := rows - nMatch - nAmtDiff - nTimeDiff
	nRightOnly := rows * 5 / 100
	totalRight := nMatch + nAmtDiff + nTimeDiff + nRightOnly

	lw, rw, cleanup, err := openCSVPair(outDir)
	if err != nil {
		return adversarialManifest{}, err
	}
	defer cleanup()

	if err := writeHeader(lw, leftHeader); err != nil {
		return adversarialManifest{}, err
	}
	if err := writeHeader(rw, rightHeader); err != nil {
		return adversarialManifest{}, err
	}

	p := buildProfile()
	rb := newRowBuilder(&p)

	var matchedLeft, matchedRight, unmatchedLeft, unmatchedRight int64
	matchOrder := fixtureOrder(nMatch, 1)
	amountDiffOrder := fixtureOrder(nAmtDiff, 2)
	timingDiffOrder := fixtureOrder(nTimeDiff, 3)
	leftOnlyOrder := fixtureOrder(nLeftOnly, 4)
	rightOnlyOrder := fixtureOrder(nRightOnly, 5)

	// Matched rows
	for i := 0; i < nMatch; i++ {
		logical := matchOrder[i]
		ref := fmt.Sprintf("URF-M-%012d", logical)
		amt := rowAmount(logical)
		matchedLeft += amt
		matchedRight += amt
		if _, err := lw.Write(rb.leftRow(ref, amt, i, i, "hint-"+ref)); err != nil {
			return adversarialManifest{}, err
		}
		if _, err := rw.Write(rb.rightRow(ref, amt, i, i)); err != nil {
			return adversarialManifest{}, err
		}
	}

	// Amount diff rows (right amount differs by 1).
	// amount_diff pairs are a separate classification — amounts are NOT added to
	// matched or unmatched monetary totals (they belong to AmountDiffTotal).
	for i := 0; i < nAmtDiff; i++ {
		logical := amountDiffOrder[i]
		ref := fmt.Sprintf("URF-A-%012d", logical)
		lAmt := rowAmount(nMatch + logical)
		rAmt := lAmt + 1
		if _, err := lw.Write(rb.leftRow(ref, lAmt, i, nMatch+i, "hint-"+ref)); err != nil {
			return adversarialManifest{}, err
		}
		if _, err := rw.Write(rb.rightRow(ref, rAmt, i, nMatch+i)); err != nil {
			return adversarialManifest{}, err
		}
	}

	// Timing diff rows (right date > 1 day ahead)
	for i := 0; i < nTimeDiff; i++ {
		logical := timingDiffOrder[i]
		ref := fmt.Sprintf("URF-T-%012d", logical)
		amt := rowAmount(nMatch + nAmtDiff + logical)
		leftDateIdx := logical % 726
		rightDateIdx := leftDateIdx + 2 // 2 days ahead, outside 1d window
		if _, err := lw.Write(rb.leftRow(ref, amt, leftDateIdx, nMatch+nAmtDiff+i, "hint-"+ref)); err != nil {
			return adversarialManifest{}, err
		}
		if _, err := rw.Write(rb.rightRow(ref, amt, rightDateIdx, nMatch+nAmtDiff+i)); err != nil {
			return adversarialManifest{}, err
		}
	}

	// Left-only rows (no right counterpart)
	for i := 0; i < nLeftOnly; i++ {
		logical := leftOnlyOrder[i]
		ref := fmt.Sprintf("URF-L-%012d", logical)
		amt := rowAmount(nMatch + nAmtDiff + nTimeDiff + logical)
		unmatchedLeft += amt
		if _, err := lw.Write(rb.leftRow(ref, amt, i, nMatch+nAmtDiff+nTimeDiff+i, "hint-"+ref)); err != nil {
			return adversarialManifest{}, err
		}
	}

	// Right-only rows (no left counterpart)
	for i := 0; i < nRightOnly; i++ {
		logical := rightOnlyOrder[i]
		ref := fmt.Sprintf("URF-R-%012d", logical)
		amt := rowAmount(logical)
		unmatchedRight += amt
		if _, err := rw.Write(rb.rightRow(ref, amt, i, nMatch+nAmtDiff+nTimeDiff+i)); err != nil {
			return adversarialManifest{}, err
		}
	}

	if err := flushAndClose(lw, rw); err != nil {
		return adversarialManifest{}, err
	}

	m := adversarialManifest{
		Scenario: "uniform_unique_refs",
		PassType: "reference_one_to_one",
		Summary: manifestSummary{
			TotalLeft:       rows,
			TotalRight:      totalRight,
			Matched:         nMatch,
			UnmatchedLeft:   nLeftOnly,
			UnmatchedRight:  nRightOnly,
			AmountDiffCount: nAmtDiff,
			TimingDiffCount: nTimeDiff,
		},
		CurrencyTotals: map[string]currencyTotals{
			"USD": {
				MatchedAmountLeft:    matchedLeft,
				MatchedAmountRight:   matchedRight,
				UnmatchedAmountLeft:  unmatchedLeft,
				UnmatchedAmountRight: unmatchedRight,
			},
		},
	}
	return m, nil
}

// ---------------------------------------------------------------------------
// Scenario: hot_skewed_refs
//
// Each left row has a unique reference. The right side has bucketSize right
// rows per left ref: exactly one exact-amount match plus (bucketSize-1)
// non-matching rows with different amounts. This stresses index bucket
// traversal in decideMatch — the engine must scan multiple right entries per
// ref to find the best candidate.
// ---------------------------------------------------------------------------

const hotBucketSize = 5

func generateHotSkewedRefs(rows int, outDir string) (adversarialManifest, error) {
	totalRight := rows * hotBucketSize
	nMatch := rows
	nRightOnly := rows * (hotBucketSize - 1)

	lw, rw, cleanup, err := openCSVPair(outDir)
	if err != nil {
		return adversarialManifest{}, err
	}
	defer cleanup()

	if err := writeHeader(lw, leftHeader); err != nil {
		return adversarialManifest{}, err
	}
	if err := writeHeader(rw, rightHeader); err != nil {
		return adversarialManifest{}, err
	}

	p := buildProfile()
	rb := newRowBuilder(&p)

	var matchedLeft, matchedRight, unmatchedRight int64
	rLineNo := 0

	for i := 0; i < rows; i++ {
		ref := fmt.Sprintf("SKW-%012d", i)
		amt := rowAmount(i)
		matchedLeft += amt
		matchedRight += amt

		if _, err := lw.Write(rb.leftRow(ref, amt, i, i, "hint-"+ref)); err != nil {
			return adversarialManifest{}, err
		}
		// First right row: exact match
		if _, err := rw.Write(rb.rightRow(ref, amt, i, rLineNo)); err != nil {
			return adversarialManifest{}, err
		}
		rLineNo++
		// Remaining right rows: non-matching amounts (at least 2x the left amount)
		for j := 1; j < hotBucketSize; j++ {
			noMatchAmt := amt*int64(j+2) + int64(j) // guaranteed different from amt
			unmatchedRight += noMatchAmt
			if _, err := rw.Write(rb.rightRow(ref, noMatchAmt, i, rLineNo)); err != nil {
				return adversarialManifest{}, err
			}
			rLineNo++
		}
	}

	if err := flushAndClose(lw, rw); err != nil {
		return adversarialManifest{}, err
	}

	return adversarialManifest{
		Scenario: "hot_skewed_refs",
		PassType: "reference_one_to_one",
		Summary: manifestSummary{
			TotalLeft:      rows,
			TotalRight:     totalRight,
			Matched:        nMatch,
			UnmatchedRight: nRightOnly,
		},
		CurrencyTotals: map[string]currencyTotals{
			"USD": {
				MatchedAmountLeft:    matchedLeft,
				MatchedAmountRight:   matchedRight,
				UnmatchedAmountRight: unmatchedRight,
			},
		},
	}, nil
}

// ---------------------------------------------------------------------------
// Scenario: high_duplicate_pressure
//
// 20% of left rows are in duplicate groups of 2 (sharing the same processor_hint
// GroupKey). These duplicate rows have no right counterparts and become
// unmatched_left. The remaining 80% matched normally. Tests the duplicate-
// detection pass (which re-scans the file for flagged GroupKeys).
// ---------------------------------------------------------------------------

func generateHighDuplicatePressure(rows int, outDir string) (adversarialManifest, error) {
	nDupLeft := rows * 20 / 100
	// Round to even (groups of 2)
	if nDupLeft%2 != 0 {
		nDupLeft--
	}
	nDupGroups := nDupLeft / 2
	nMatch := rows - nDupLeft
	nRightOnly := 0
	totalRight := nMatch + nRightOnly

	lw, rw, cleanup, err := openCSVPair(outDir)
	if err != nil {
		return adversarialManifest{}, err
	}
	defer cleanup()

	if err := writeHeader(lw, leftHeader); err != nil {
		return adversarialManifest{}, err
	}
	if err := writeHeader(rw, rightHeader); err != nil {
		return adversarialManifest{}, err
	}

	p := buildProfile()
	rb := newRowBuilder(&p)

	var matchedLeft, matchedRight, unmatchedLeft int64
	lineNo := 0

	// Normal matched rows (unique refs, unique processor_hint)
	for i := 0; i < nMatch; i++ {
		ref := fmt.Sprintf("DUP-NORM-%012d", i)
		amt := rowAmount(i)
		matchedLeft += amt
		matchedRight += amt
		hint := "norm-hint-" + strconv.Itoa(i)
		if _, err := lw.Write(rb.leftRow(ref, amt, i, lineNo, hint)); err != nil {
			return adversarialManifest{}, err
		}
		if _, err := rw.Write(rb.rightRow(ref, amt, i, i)); err != nil {
			return adversarialManifest{}, err
		}
		lineNo++
	}

	// Duplicate group rows (shared processor_hint, no right counterpart)
	for g := 0; g < nDupGroups; g++ {
		groupHint := fmt.Sprintf("DUP-GROUP-%012d", g)
		for k := 0; k < 2; k++ {
			ref := fmt.Sprintf("DUP-LEFT-%012d-R%d", g, k)
			amt := rowAmount(nMatch + g*2 + k)
			unmatchedLeft += amt
			if _, err := lw.Write(rb.leftRow(ref, amt, g, lineNo, groupHint)); err != nil {
				return adversarialManifest{}, err
			}
			lineNo++
		}
	}

	if err := flushAndClose(lw, rw); err != nil {
		return adversarialManifest{}, err
	}

	return adversarialManifest{
		Scenario: "high_duplicate_pressure",
		PassType: "reference_one_to_one",
		Summary: manifestSummary{
			TotalLeft:      rows,
			TotalRight:     totalRight,
			Matched:        nMatch,
			UnmatchedLeft:  nDupLeft,
			DuplicateCount: nDupLeft, // all dup rows counted (2 per group)
		},
		CurrencyTotals: map[string]currencyTotals{
			"USD": {
				MatchedAmountLeft:   matchedLeft,
				MatchedAmountRight:  matchedRight,
				UnmatchedAmountLeft: unmatchedLeft,
			},
		},
	}, nil
}

// ---------------------------------------------------------------------------
// Scenario: high_result_emission
//
// 99% match rate with 0.5% amount_diff and 0.5% timing_diff. Maximum write
// calls to ResultWriter per input row, testing output I/O throughput.
// ---------------------------------------------------------------------------

func generateHighResultEmission(rows int, outDir string) (adversarialManifest, error) {
	nMatch := rows * 99 / 100
	nAmtDiff := rows / 200 // ~0.5%
	nTimeDiff := rows - nMatch - nAmtDiff
	totalRight := rows

	lw, rw, cleanup, err := openCSVPair(outDir)
	if err != nil {
		return adversarialManifest{}, err
	}
	defer cleanup()

	if err := writeHeader(lw, leftHeader); err != nil {
		return adversarialManifest{}, err
	}
	if err := writeHeader(rw, rightHeader); err != nil {
		return adversarialManifest{}, err
	}

	p := buildProfile()
	rb := newRowBuilder(&p)

	var matchedLeft, matchedRight int64
	rLineNo := 0

	for i := 0; i < nMatch; i++ {
		ref := fmt.Sprintf("HRE-M-%012d", i)
		amt := rowAmount(i)
		matchedLeft += amt
		matchedRight += amt
		if _, err := lw.Write(rb.leftRow(ref, amt, i, i, "hint-"+ref)); err != nil {
			return adversarialManifest{}, err
		}
		if _, err := rw.Write(rb.rightRow(ref, amt, i, rLineNo)); err != nil {
			return adversarialManifest{}, err
		}
		rLineNo++
	}

	for i := 0; i < nAmtDiff; i++ {
		ref := fmt.Sprintf("HRE-A-%012d", i)
		lAmt := rowAmount(nMatch + i)
		rAmt := lAmt + 1
		if _, err := lw.Write(rb.leftRow(ref, lAmt, i, nMatch+i, "hint-"+ref)); err != nil {
			return adversarialManifest{}, err
		}
		if _, err := rw.Write(rb.rightRow(ref, rAmt, i, rLineNo)); err != nil {
			return adversarialManifest{}, err
		}
		rLineNo++
	}

	for i := 0; i < nTimeDiff; i++ {
		ref := fmt.Sprintf("HRE-T-%012d", i)
		amt := rowAmount(nMatch + nAmtDiff + i)
		leftDateIdx := i % 726
		rightDateIdx := leftDateIdx + 2
		if _, err := lw.Write(rb.leftRow(ref, amt, leftDateIdx, nMatch+nAmtDiff+i, "hint-"+ref)); err != nil {
			return adversarialManifest{}, err
		}
		if _, err := rw.Write(rb.rightRow(ref, amt, rightDateIdx, rLineNo)); err != nil {
			return adversarialManifest{}, err
		}
		rLineNo++
	}

	if err := flushAndClose(lw, rw); err != nil {
		return adversarialManifest{}, err
	}

	return adversarialManifest{
		Scenario: "high_result_emission",
		PassType: "reference_one_to_one",
		Summary: manifestSummary{
			TotalLeft:       rows,
			TotalRight:      totalRight,
			Matched:         nMatch,
			AmountDiffCount: nAmtDiff,
			TimingDiffCount: nTimeDiff,
		},
		CurrencyTotals: map[string]currencyTotals{
			"USD": {
				MatchedAmountLeft:  matchedLeft,
				MatchedAmountRight: matchedRight,
			},
		},
	}, nil
}

// ---------------------------------------------------------------------------
// Scenario: one_to_many_settlement
//
// 1 left row matched against rightsPerLeft right rows sharing the same
// reference. Each right amount = leftAmount / rightsPerLeft (integer division).
// The one_to_many pass compares sum(right.amounts) against left.amount with
// tolerance 0; amounts are constructed to sum exactly.
//
// Right parser must use duplicate_policy: keep to suppress right-side
// duplicate detection (rightsPerLeft right rows share the same Reference →
// GroupKey, triggering the flag policy).
// ---------------------------------------------------------------------------

const oneToManyRightsPerLeft = 3

func generateOneToManySettlement(rows int, outDir string) (adversarialManifest, error) {
	// Each left row has amount = unit * rightsPerLeft; each right has amount = unit.
	const unit = 1000
	const leftAmt = unit * oneToManyRightsPerLeft

	totalLeft := rows
	totalRight := rows * oneToManyRightsPerLeft

	lw, rw, cleanup, err := openCSVPair(outDir)
	if err != nil {
		return adversarialManifest{}, err
	}
	defer cleanup()

	if err := writeHeader(lw, leftHeader); err != nil {
		return adversarialManifest{}, err
	}
	if err := writeHeader(rw, rightHeader); err != nil {
		return adversarialManifest{}, err
	}

	p := buildProfile()
	rb := newRowBuilder(&p)

	var matchedLeft, matchedRight int64
	rLineNo := 0

	for i := 0; i < rows; i++ {
		ref := fmt.Sprintf("OTM-%012d", i)
		matchedLeft += leftAmt
		if _, err := lw.Write(rb.leftRow(ref, leftAmt, i, i, "hint-"+ref)); err != nil {
			return adversarialManifest{}, err
		}
		for j := 0; j < oneToManyRightsPerLeft; j++ {
			matchedRight += unit
			if _, err := rw.Write(rb.rightRow(ref, unit, i, rLineNo)); err != nil {
				return adversarialManifest{}, err
			}
			rLineNo++
		}
	}

	if err := flushAndClose(lw, rw); err != nil {
		return adversarialManifest{}, err
	}

	return adversarialManifest{
		Scenario: "one_to_many_settlement",
		PassType: "one_to_many",
		Summary: manifestSummary{
			TotalLeft:           totalLeft,
			TotalRight:          totalRight,
			GroupedMatchedCount: rows,
		},
		CurrencyTotals: map[string]currencyTotals{
			"USD": {
				MatchedAmountLeft:  matchedLeft,
				MatchedAmountRight: matchedRight,
			},
		},
		SkippedMatrix: []skippedEntry{
			{Format: "csv", Reason: "grouped match events not supported in CSV format"},
			{Format: "table", Reason: "grouped match events not supported in table format"},
			{Backend: "disk", Reason: "disk backend supports reference_one_to_one only"},
			{Backend: "partitioned", Reason: "partitioned backend supports reference_one_to_one only"},
		},
	}, nil
}

// ---------------------------------------------------------------------------
// Scenario: many_to_many_settlement
//
// Groups of leftPerGroup left rows share the same reference. Groups of
// rightPerGroup right rows share the same reference. The many_to_many pass
// groups both sides by Reference (default group_by) and matches groups where
// sum(left.amounts) == sum(right.amounts).
//
// Both parsers must use duplicate_policy: keep because rows within each group
// share GroupKey (which falls back to Reference when group_col is unset),
// which would otherwise trigger duplicate detection.
// ---------------------------------------------------------------------------

const (
	manyToManyLeftPerGroup  = 3
	manyToManyRightPerGroup = 3
	manyToManyUnit          = 1000
)

func generateManyToManySettlement(rows int, outDir string) (adversarialManifest, error) {
	// Ensure rows is a multiple of leftPerGroup so all groups are complete.
	nGroups := rows / manyToManyLeftPerGroup
	if nGroups < 1 {
		nGroups = 1
	}
	totalLeft := nGroups * manyToManyLeftPerGroup
	totalRight := nGroups * manyToManyRightPerGroup

	lw, rw, cleanup, err := openCSVPair(outDir)
	if err != nil {
		return adversarialManifest{}, err
	}
	defer cleanup()

	if err := writeHeader(lw, leftHeader); err != nil {
		return adversarialManifest{}, err
	}
	if err := writeHeader(rw, rightHeader); err != nil {
		return adversarialManifest{}, err
	}

	p := buildProfile()
	rb := newRowBuilder(&p)

	var matchedLeft, matchedRight int64
	lLineNo, rLineNo := 0, 0

	for g := 0; g < nGroups; g++ {
		// All rows in the group share the same ref so many_to_many can group them.
		groupRef := fmt.Sprintf("MTM-GROUP-%012d", g)
		hint := "mtm-hint-" + strconv.Itoa(g)

		for k := 0; k < manyToManyLeftPerGroup; k++ {
			matchedLeft += manyToManyUnit
			if _, err := lw.Write(rb.leftRow(groupRef, manyToManyUnit, g, lLineNo, hint)); err != nil {
				return adversarialManifest{}, err
			}
			lLineNo++
		}
		for k := 0; k < manyToManyRightPerGroup; k++ {
			matchedRight += manyToManyUnit
			if _, err := rw.Write(rb.rightRow(groupRef, manyToManyUnit, g, rLineNo)); err != nil {
				return adversarialManifest{}, err
			}
			rLineNo++
		}
	}

	if err := flushAndClose(lw, rw); err != nil {
		return adversarialManifest{}, err
	}

	return adversarialManifest{
		Scenario: "many_to_many_settlement",
		PassType: "many_to_many",
		Summary: manifestSummary{
			TotalLeft:              totalLeft,
			TotalRight:             totalRight,
			ManyToManyMatchedCount: nGroups,
		},
		CurrencyTotals: map[string]currencyTotals{
			"USD": {
				MatchedAmountLeft:  matchedLeft,
				MatchedAmountRight: matchedRight,
			},
		},
		SkippedMatrix: []skippedEntry{
			{Format: "csv", Reason: "grouped match events not supported in CSV format"},
			{Format: "table", Reason: "grouped match events not supported in table format"},
			{Backend: "disk", Reason: "disk backend supports reference_one_to_one only"},
			{Backend: "partitioned", Reason: "partitioned backend supports reference_one_to_one only"},
		},
	}, nil
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

func main() {
	scenario := flag.String("scenario", "", "scenario name (required)")
	rows := flag.Int("rows", 100000, "left-side row count")
	outDir := flag.String("output-dir", "", "output directory (default /tmp/adversarial/<scenario>)")
	seed := flag.Int64("seed", 42, "seed for deterministic fixture ordering and values")
	flag.Parse()

	if *scenario == "" {
		log.Fatal("--scenario is required; choose one of: " +
			"uniform_unique_refs, hot_skewed_refs, high_duplicate_pressure, " +
			"high_result_emission, one_to_many_settlement, many_to_many_settlement")
	}
	if *rows < 10 {
		log.Fatalf("--rows must be >= 10 (got %d)", *rows)
	}

	dir := *outDir
	if dir == "" {
		dir = filepath.Join("/tmp/adversarial", *scenario)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Fatalf("mkdir %s: %v", dir, err)
	}

	generationSeed = *seed

	log.Printf("==> adversarial scenario: %s  rows: %d  seed: %d", *scenario, *rows, *seed)
	start := time.Now()

	var (
		m   adversarialManifest
		err error
	)
	switch *scenario {
	case "uniform_unique_refs":
		m, err = generateUniformUniqueRefs(*rows, dir)
	case "hot_skewed_refs":
		m, err = generateHotSkewedRefs(*rows, dir)
	case "high_duplicate_pressure":
		m, err = generateHighDuplicatePressure(*rows, dir)
	case "high_result_emission":
		m, err = generateHighResultEmission(*rows, dir)
	case "one_to_many_settlement":
		m, err = generateOneToManySettlement(*rows, dir)
	case "many_to_many_settlement":
		m, err = generateManyToManySettlement(*rows, dir)
	default:
		log.Fatalf("unknown scenario %q", *scenario)
	}
	if err != nil {
		log.Fatalf("generate %s: %v", *scenario, err)
	}

	m.Seed = *seed
	m.Rows = *rows

	if err := writeManifest(dir, m); err != nil {
		log.Fatalf("write manifest: %v", err)
	}

	log.Printf("done in %v → %s", time.Since(start).Round(time.Millisecond), dir)
	log.Printf("  left.csv   total_left=%d", m.Summary.TotalLeft)
	log.Printf("  right.csv  total_right=%d", m.Summary.TotalRight)
	log.Printf("  manifest.json")
}
