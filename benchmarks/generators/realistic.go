//go:build ignore

package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

var ledgerHeader = []string{
	"booking_date", "description", "settlement_amount_minor", "recon_ref", "currency",
	"order_id", "invoice_id", "customer_email", "event_type", "status",
	"gross_minor", "fee_minor", "tax_minor", "net_minor", "processor_id",
	"payout_id", "group_key", "metadata",
}

var providerHeader = []string{
	"report_date", "report_amount_minor", "report_ref", "descriptor", "report_currency",
	"transaction_id", "source_id", "reporting_category", "debit_credit", "gross_minor",
	"fee_minor", "net_minor", "available_on", "payout_id", "account_id",
	"status", "timezone", "raw_note",
}

type scenarioSpec struct {
	name            string
	sources         int
	matchPct        int
	amountPct       int
	timingPct       int
	rightOnlyPct    int
	rightOnlyRatio  int
	duplicateGroups int
}

type manifest struct {
	Scenario        string         `json:"scenario"`
	Summary         manifestCounts `json:"summary"`
	DuplicateGroups int            `json:"duplicate_groups"`
}

type manifestCounts struct {
	TotalLeft       int `json:"total_left"`
	TotalRight      int `json:"total_right"`
	Matched         int `json:"matched"`
	UnmatchedLeft   int `json:"unmatched_left"`
	UnmatchedRight  int `json:"unmatched_right"`
	AmountDiffCount int `json:"amount_diff_count"`
	TimingDiffCount int `json:"timing_diff_count"`
	DuplicateCount  int `json:"duplicate_count"`
}

type rightFile struct {
	name   string
	file   *os.File
	writer *csv.Writer
	rows   int
}

func main() {
	rows := flag.Int("rows", 100_000, "left rows")
	outDir := flag.String("out", "benchmarks/.out/realistic", "output directory")
	scenarioName := flag.String("scenario", "payout_reconciliation", "scenario name")
	flag.Parse()

	spec, ok := scenarios()[*scenarioName]
	if !ok {
		log.Fatalf("unknown scenario %q", *scenarioName)
	}
	if *rows < 100 {
		log.Fatalf("-rows must be >= 100")
	}
	if spec.duplicateGroups*2 > *rows/2 {
		spec.duplicateGroups = *rows / 4
	}

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		log.Fatal(err)
	}
	if err := generate(*rows, *outDir, spec); err != nil {
		log.Fatal(err)
	}
}

func scenarios() map[string]scenarioSpec {
	return map[string]scenarioSpec{
		"payout_reconciliation":        {name: "payout_reconciliation", sources: 1, matchPct: 90, amountPct: 2, timingPct: 3, rightOnlyPct: 2},
		"bank_statement_noise":         {name: "bank_statement_noise", sources: 1, matchPct: 70, amountPct: 5, timingPct: 10, rightOnlyPct: 5},
		"refunds_disputes_chargebacks": {name: "refunds_disputes_chargebacks", sources: 2, matchPct: 75, amountPct: 5, timingPct: 5, rightOnlyPct: 10},
		"multi_currency_settlement":    {name: "multi_currency_settlement", sources: 1, matchPct: 80, amountPct: 5, timingPct: 5, rightOnlyPct: 5},
		"split_provider_exports":       {name: "split_provider_exports", sources: 3, matchPct: 78, amountPct: 4, timingPct: 8, rightOnlyPct: 5},
		"high_noise_right_side":        {name: "high_noise_right_side", sources: 3, matchPct: 75, amountPct: 5, timingPct: 5, rightOnlyRatio: 1},
		"duplicate_replay_window":      {name: "duplicate_replay_window", sources: 2, matchPct: 80, amountPct: 5, timingPct: 5, rightOnlyPct: 5, duplicateGroups: 100},
		"late_settlement_window":       {name: "late_settlement_window", sources: 2, matchPct: 65, amountPct: 5, timingPct: 20, rightOnlyPct: 5},
	}
}

func generate(rows int, outDir string, spec scenarioSpec) error {
	matched := pct(rows, spec.matchPct)
	amountDiff := pct(rows, spec.amountPct)
	timingDiff := pct(rows, spec.timingPct)
	unmatchedLeft := rows - matched - amountDiff - timingDiff
	matchableRight := matched + amountDiff + timingDiff
	unmatchedRight := pct(rows, spec.rightOnlyPct)
	if spec.rightOnlyRatio > 0 {
		unmatchedRight = matchableRight * spec.rightOnlyRatio
	}

	leftPath := filepath.Join(outDir, "ledger.csv")
	lf, err := os.Create(leftPath)
	if err != nil {
		return err
	}
	defer lf.Close()
	lw := csv.NewWriter(lf)
	if err := lw.Write(ledgerHeader); err != nil {
		return err
	}

	rights, err := openRightFiles(outDir, spec.sources)
	if err != nil {
		return err
	}
	defer closeRightFiles(rights)
	for i := range rights {
		if err := rights[i].writer.Write(providerHeader); err != nil {
			return err
		}
	}

	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	matchIndex := 0
	writeRight := func(group int, row []string) error {
		idx := group % len(rights)
		rights[idx].rows++
		return rights[idx].writer.Write(row)
	}

	for i := 0; i < matched; i++ {
		ref := ref("PAY", i)
		group := groupKey(spec, i, ref)
		date := base.AddDate(0, 0, i%60)
		amount := int64(2500 + i%900_000)
		if err := lw.Write(ledgerRow(spec.name, date, amount, ref, group, i, "payment", "posted")); err != nil {
			return err
		}
		if err := writeRight(matchIndex, providerRow(spec.name, date, amount, ref, i, "charge", "credit")); err != nil {
			return err
		}
		matchIndex++
	}
	for i := 0; i < amountDiff; i++ {
		ref := ref("AMT", i)
		date := base.AddDate(0, 0, i%60)
		amount := int64(3000 + i%700_000)
		if err := lw.Write(ledgerRow(spec.name, date, amount, ref, uniqueGroup(ref), i, "fee_adjustment", "posted")); err != nil {
			return err
		}
		if err := writeRight(matchIndex, providerRow(spec.name, date, amount+int64(11+i%9), ref, i, "fee", "debit")); err != nil {
			return err
		}
		matchIndex++
	}
	for i := 0; i < timingDiff; i++ {
		ref := ref("LATE", i)
		date := base.AddDate(0, 0, i%60)
		amount := int64(4000 + i%800_000)
		if err := lw.Write(ledgerRow(spec.name, date, amount, ref, uniqueGroup(ref), i, "settlement", "posted")); err != nil {
			return err
		}
		if err := writeRight(matchIndex, providerRow(spec.name, date.AddDate(0, 0, 3+i%3), amount, ref, i, "payout", "credit")); err != nil {
			return err
		}
		matchIndex++
	}
	for i := 0; i < unmatchedLeft; i++ {
		ref := ref("LEDGERONLY", i)
		date := base.AddDate(0, 0, i%60)
		amount := int64(5000 + i%600_000)
		if err := lw.Write(ledgerRow(spec.name, date, amount, ref, uniqueGroup(ref), i, "manual_journal", "posted")); err != nil {
			return err
		}
	}
	for i := 0; i < unmatchedRight; i++ {
		ref := ref("PROVIDERONLY", i)
		date := base.AddDate(0, 0, i%60)
		amount := int64(6000 + i%500_000)
		if err := writeRight(i, providerRow(spec.name, date, amount, ref, i, rightOnlyCategory(spec.name, i), "debit")); err != nil {
			return err
		}
	}

	lw.Flush()
	if err := lw.Error(); err != nil {
		return err
	}
	for i := range rights {
		rights[i].writer.Flush()
		if err := rights[i].writer.Error(); err != nil {
			return err
		}
	}

	m := manifest{
		Scenario: spec.name,
		Summary: manifestCounts{
			TotalLeft:       rows,
			TotalRight:      matchableRight + unmatchedRight,
			Matched:         matched,
			UnmatchedLeft:   unmatchedLeft,
			UnmatchedRight:  unmatchedRight,
			AmountDiffCount: amountDiff,
			TimingDiffCount: timingDiff,
			DuplicateCount:  spec.duplicateGroups * 2,
		},
		DuplicateGroups: spec.duplicateGroups,
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outDir, "expected.json"), append(data, '\n'), 0o644); err != nil {
		return err
	}

	log.Printf("generated realistic scenario=%s rows=%d left=%s right_rows=%d sources=%d", spec.name, rows, leftPath, m.Summary.TotalRight, spec.sources)
	return nil
}

func pct(n, percent int) int {
	return n * percent / 100
}

func openRightFiles(outDir string, sources int) ([]rightFile, error) {
	files := make([]rightFile, sources)
	for i := range files {
		name := "provider.csv"
		if sources > 1 {
			name = fmt.Sprintf("provider_%d.csv", i+1)
		}
		path := filepath.Join(outDir, name)
		f, err := os.Create(path)
		if err != nil {
			closeRightFiles(files[:i])
			return nil, err
		}
		files[i] = rightFile{name: path, file: f, writer: csv.NewWriter(f)}
	}
	return files, nil
}

func closeRightFiles(files []rightFile) {
	for i := range files {
		if files[i].file != nil {
			_ = files[i].file.Close()
		}
	}
}

func ref(prefix string, i int) string {
	return fmt.Sprintf("%s-%012d", prefix, i)
}

func uniqueGroup(ref string) string {
	return "GROUP-" + ref
}

func groupKey(spec scenarioSpec, i int, ref string) string {
	if spec.duplicateGroups > 0 && i < spec.duplicateGroups*2 {
		return fmt.Sprintf("DUP-%012d", i/2)
	}
	return uniqueGroup(ref)
}

func ledgerRow(scenario string, date time.Time, amount int64, ref string, group string, i int, eventType string, status string) []string {
	gross := amount + int64(30+i%25)
	fee := int64(30 + i%25)
	tax := int64(i % 7)
	currency := currencyFor(scenario, i)
	return []string{
		date.Format("2006-01-02"),
		messyDescription(scenario, ref, i),
		strconv.FormatInt(amount, 10),
		ref,
		currency,
		fmt.Sprintf("ord_%08d", i),
		fmt.Sprintf("INV-%06d", i/3),
		fmt.Sprintf("customer.%d@example.test", i%2000),
		eventType,
		status,
		strconv.FormatInt(gross, 10),
		strconv.FormatInt(fee, 10),
		strconv.FormatInt(tax, 10),
		strconv.FormatInt(amount, 10),
		fmt.Sprintf("pi_%012d", i),
		fmt.Sprintf("po_%06d", i/500),
		group,
		fmt.Sprintf(`{"source":"%s","store":"%s","note":"quoted memo %d"}`, scenario, store(i), i),
	}
}

func providerRow(scenario string, date time.Time, amount int64, ref string, i int, category string, direction string) []string {
	fee := int64(30 + i%25)
	return []string{
		date.Format("2006-01-02"),
		strconv.FormatInt(amount, 10),
		ref,
		providerDescriptor(scenario, ref, i),
		currencyFor(scenario, i),
		fmt.Sprintf("txn_%012d", i),
		fmt.Sprintf("src_%012d", i),
		category,
		direction,
		strconv.FormatInt(amount+fee, 10),
		strconv.FormatInt(fee, 10),
		strconv.FormatInt(amount, 10),
		date.AddDate(0, 0, 2).Format("2006-01-02"),
		fmt.Sprintf("po_%06d", i/500),
		fmt.Sprintf("acct_%04d", i%25),
		statusFor(category, i),
		timezoneFor(i),
		fmt.Sprintf("raw export row, contains comma, quote \"marker\" %d", i),
	}
}

func messyDescription(scenario, ref string, i int) string {
	switch scenario {
	case "bank_statement_noise":
		return fmt.Sprintf("CARD SETTLEMENT, %s  ****%04d  Store \"%s\"", ref, i%10000, store(i))
	case "refunds_disputes_chargebacks":
		return fmt.Sprintf("Refund/dispute workflow for %s, case #%06d", ref, i)
	default:
		return fmt.Sprintf("Ledger booking %s, %s", ref, store(i))
	}
}

func providerDescriptor(scenario, ref string, i int) string {
	switch scenario {
	case "bank_statement_noise":
		return fmt.Sprintf("STRP*%s, ONLINE #%04d", store(i), i%10000)
	case "multi_currency_settlement":
		return fmt.Sprintf("FX settlement %s rate=%d.%04d", ref, 1+i%2, i%10000)
	default:
		return fmt.Sprintf("Provider item \"%s\", batch %03d", ref, i%365)
	}
}

func rightOnlyCategory(scenario string, i int) string {
	switch scenario {
	case "refunds_disputes_chargebacks":
		cats := []string{"refund", "dispute", "chargeback_fee", "reversal"}
		return cats[i%len(cats)]
	case "high_noise_right_side":
		cats := []string{"reserve_transaction", "balance_transfer", "payout_fee", "adjustment"}
		return cats[i%len(cats)]
	default:
		return "adjustment"
	}
}

func statusFor(category string, i int) string {
	if category == "dispute" || category == "chargeback_fee" {
		return "needs_review"
	}
	if i%19 == 0 {
		return "pending"
	}
	return "posted"
}

func currencyFor(_ string, _ int) string {
	return "USD"
}

func timezoneFor(i int) string {
	zones := []string{"UTC", "America/New_York", "Europe/London", "Asia/Singapore"}
	return zones[i%len(zones)]
}

func store(i int) string {
	stores := []string{"North Market", "SaaS Desk", "Retail Ops", "Global Web", "Partner Channel"}
	return stores[i%len(stores)]
}
