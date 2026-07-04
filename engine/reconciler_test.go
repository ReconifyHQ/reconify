package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/reconifyhq/reconify/config"
)

// makeTx is a test helper that returns a Transaction with the minimum fields set.
func makeTx(id string, amount int64, ref string) Transaction {
	return Transaction{
		ID:        id,
		Date:      time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Amount:    amount,
		Currency:  "USD",
		Reference: ref,
		Name:      "Test " + id,
		Source:    "test",
	}
}

func TestReconcile_FailsOnMixedCurrency(t *testing.T) {
	left := []Transaction{makeTx("l1", 100, "REF-1")}
	right := []Transaction{makeTx("r1", 100, "REF-1")}
	right[0].Currency = "EUR"

	_, err := Reconcile("p", "left", "right", left, right, config.Pair{DateWindow: "0d"})
	if err == nil {
		t.Fatal("expected mixed-currency error, got nil")
	}
	if !strings.Contains(err.Error(), "mixed currencies") {
		t.Fatalf("expected mixed-currency error, got: %v", err)
	}
}

func TestReconcileStreaming_MonetaryTotalsInvariant(t *testing.T) {
	dir := t.TempDir()
	leftPath := filepath.Join(dir, "left.csv")
	rightPath := filepath.Join(dir, "right.csv")

	leftCSV := strings.Join([]string{
		"id,date,amount,currency,reference,name",
		"l1,2024-01-01,1.00,USD,REF-MATCH,Left Match",
		"l2,2024-01-01,2.00,USD,REF-DIFF,Left Diff",
		"l3,2024-01-01,0.50,USD,REF-L_ONLY,Left Only",
	}, "\n") + "\n"
	rightCSV := strings.Join([]string{
		"id,date,amount,currency,reference,name",
		"r1,2024-01-01,1.00,USD,REF-MATCH,Right Match",
		"r2,2024-01-01,2.20,USD,REF-DIFF,Right Diff",
		"r3,2024-01-01,0.80,USD,REF-RONLY,Right Only",
	}, "\n") + "\n"

	if err := os.WriteFile(leftPath, []byte(leftCSV), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rightPath, []byte(rightCSV), 0600); err != nil {
		t.Fatal(err)
	}

	parserCfg := config.CSVParserCfg{
		Type:        "csv",
		DateCol:     "date",
		DateLayout:  "2006-01-02",
		AmountCol:   "amount",
		Decimal:     ".",
		Thousands:   ",",
		Multiplier:  100,
		CurrencyCol: "currency",
		RefCol:      "reference",
		NameCol:     "name",
	}
	pair := config.Pair{DateWindow: "0d", AmountToleranceMinor: 0, NameMode: "none"}

	var out bytes.Buffer
	w := newJSONWriter(&out)
	w.SetMeta("p", "left", "right")
	idx := NewMemoryIndex()
	defer idx.Close()

	if err := ReconcileStreaming(
		context.Background(),
		"p",
		"left",
		"right",
		leftPath,
		rightPath,
		parserCfg,
		parserCfg,
		pair,
		idx,
		w,
		0,
	); err != nil {
		t.Fatalf("ReconcileStreaming error: %v", err)
	}

	var res Result
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	s := res.Summary
	if s.AmountDiffTotal != 20 {
		t.Errorf("AmountDiffTotal = %d, want 20", s.AmountDiffTotal)
	}
	if s.UnmatchedAmountLeft != 50 {
		t.Errorf("UnmatchedAmountLeft = %d, want 50", s.UnmatchedAmountLeft)
	}
	if s.UnmatchedAmountRight != 80 {
		t.Errorf("UnmatchedAmountRight = %d, want 80", s.UnmatchedAmountRight)
	}
	if s.TotalDiscrepancy != 150 {
		t.Errorf("TotalDiscrepancy = %d, want 150", s.TotalDiscrepancy)
	}
	computed := s.UnmatchedAmountLeft + s.UnmatchedAmountRight + s.AmountDiffTotal
	if s.TotalDiscrepancy != computed {
		t.Errorf("TotalDiscrepancy invariant broken: got %d, computed %d", s.TotalDiscrepancy, computed)
	}
}

func TestReconcileStreaming_MixedJSONAndXLSXInputs(t *testing.T) {
	dir := t.TempDir()
	leftPath := filepath.Join(dir, "left.json")
	rightPath := filepath.Join(dir, "right.xlsx")

	leftJSON := `[
		{"date":"2024-01-01","amount":"1.00","currency":"USD","ref_id":"REF-MATCH","description":"Left Match"},
		{"date":"2024-01-01","amount":"2.00","currency":"USD","ref_id":"REF-LEFT","description":"Left Only"}
	]`
	if err := os.WriteFile(leftPath, []byte(leftJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	writeWorkbook(t, rightPath, "Transactions", [][]string{
		{"date", "amount", "currency", "ref_id", "description"},
		{"2024-01-01", "1.00", "USD", "REF-MATCH", "Right Match"},
		{"2024-01-01", "3.00", "USD", "REF-RIGHT", "Right Only"},
	})

	leftCfg := baseParserCfg("json")
	rightCfg := baseParserCfg("xlsx")
	rightCfg.Sheet = "Transactions"
	pair := config.Pair{DateWindow: "0d", AmountToleranceMinor: 0, NameMode: "none"}

	var out bytes.Buffer
	w := newJSONWriter(&out)
	w.SetMeta("p", "left", "right")
	idx := NewMemoryIndex()
	defer idx.Close()

	if err := ReconcileStreaming(
		context.Background(),
		"p",
		"left",
		"right",
		leftPath,
		rightPath,
		leftCfg,
		rightCfg,
		pair,
		idx,
		w,
		0,
	); err != nil {
		t.Fatalf("ReconcileStreaming error: %v", err)
	}

	var res Result
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if res.Summary.MatchedCount != 1 || res.Summary.UnmatchedLeft != 1 || res.Summary.UnmatchedRight != 1 {
		t.Fatalf("unexpected summary: %+v", res.Summary)
	}
}

func TestReconcileStreaming_FailsOnMixedCurrency(t *testing.T) {
	dir := t.TempDir()
	leftPath := filepath.Join(dir, "left.csv")
	rightPath := filepath.Join(dir, "right.csv")

	leftCSV := "id,date,amount,currency,reference,name\nl1,2024-01-01,1.00,USD,REF-1,Left\n"
	rightCSV := "id,date,amount,currency,reference,name\nr1,2024-01-01,1.00,EUR,REF-1,Right\n"

	if err := os.WriteFile(leftPath, []byte(leftCSV), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rightPath, []byte(rightCSV), 0600); err != nil {
		t.Fatal(err)
	}

	parserCfg := config.CSVParserCfg{
		Type:        "csv",
		DateCol:     "date",
		DateLayout:  "2006-01-02",
		AmountCol:   "amount",
		Decimal:     ".",
		Thousands:   ",",
		Multiplier:  100,
		CurrencyCol: "currency",
		RefCol:      "reference",
		NameCol:     "name",
	}

	var out bytes.Buffer
	w := newJSONWriter(&out)
	idx := NewMemoryIndex()
	defer idx.Close()

	err := ReconcileStreaming(
		context.Background(),
		"p",
		"left",
		"right",
		leftPath,
		rightPath,
		parserCfg,
		parserCfg,
		config.Pair{DateWindow: "0d", AmountToleranceMinor: 0, NameMode: "none"},
		idx,
		w,
		0,
	)
	if err == nil {
		t.Fatal("expected mixed-currency error, got nil")
	}
	if !strings.Contains(err.Error(), "mixed currencies") {
		t.Fatalf("expected mixed-currency error, got: %v", err)
	}
}

func TestReconcileStreaming_DiskIndexMatchesMemorySemantics(t *testing.T) {
	dir := t.TempDir()
	leftPath := filepath.Join(dir, "left.csv")
	rightPath := filepath.Join(dir, "right.csv")

	leftCSV := strings.Join([]string{
		"id,date,amount,currency,reference,name",
		"l1,2024-01-01,1.00,USD,REF-1,Left 1",
		"l2,2024-01-01,2.00,USD,REF-2,Left 2",
		"l3,2024-01-01,3.00,USD,REF-ONLY-L,Left 3",
	}, "\n") + "\n"
	rightCSV := strings.Join([]string{
		"id,date,amount,currency,reference,name",
		"r1,2024-01-01,1.00,USD,REF-1,Right 1",
		"r2,2024-01-01,2.20,USD,REF-2,Right 2",
		"r3,2024-01-01,4.00,USD,REF-ONLY-R,Right 3",
	}, "\n") + "\n"

	if err := os.WriteFile(leftPath, []byte(leftCSV), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rightPath, []byte(rightCSV), 0600); err != nil {
		t.Fatal(err)
	}

	parserCfg := config.CSVParserCfg{
		Type:        "csv",
		DateCol:     "date",
		DateLayout:  "2006-01-02",
		AmountCol:   "amount",
		Decimal:     ".",
		Thousands:   ",",
		Multiplier:  100,
		CurrencyCol: "currency",
		RefCol:      "reference",
		NameCol:     "name",
	}

	var out bytes.Buffer
	w := newJSONWriter(&out)
	diskIdx, err := NewDiskIndex(t.TempDir())
	if err != nil {
		t.Fatalf("NewDiskIndex: %v", err)
	}
	defer diskIdx.Close()

	if err := ReconcileStreaming(
		context.Background(),
		"p",
		"left",
		"right",
		leftPath,
		rightPath,
		parserCfg,
		parserCfg,
		config.Pair{DateWindow: "0d", AmountToleranceMinor: 0, NameMode: "none"},
		diskIdx,
		w,
		0,
	); err != nil {
		t.Fatalf("ReconcileStreaming error: %v", err)
	}

	var res Result
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if res.Summary.MatchedCount != 1 {
		t.Fatalf("MatchedCount=%d, want 1", res.Summary.MatchedCount)
	}
	if res.Summary.AmountDiffCount != 1 {
		t.Fatalf("AmountDiffCount=%d, want 1", res.Summary.AmountDiffCount)
	}
	if res.Summary.UnmatchedLeft != 1 {
		t.Fatalf("UnmatchedLeft=%d, want 1", res.Summary.UnmatchedLeft)
	}
	if res.Summary.UnmatchedRight != 1 {
		t.Fatalf("UnmatchedRight=%d, want 1", res.Summary.UnmatchedRight)
	}
}

func TestReconcile_MonetaryTotals_AllMatched(t *testing.T) {
	left := []Transaction{
		makeTx("l1", 100, "REF-1"),
		makeTx("l2", 200, "REF-2"),
	}
	right := []Transaction{
		makeTx("r1", 100, "REF-1"),
		makeTx("r2", 200, "REF-2"),
	}
	pair := config.Pair{DateWindow: "0d", AmountToleranceMinor: 0}

	res, err := Reconcile("p", "left", "right", left, right, pair)
	if err != nil {
		t.Fatal(err)
	}

	s := res.Summary
	if s.MatchedAmountLeft != 300 {
		t.Errorf("MatchedAmountLeft = %d, want 300", s.MatchedAmountLeft)
	}
	if s.MatchedAmountRight != 300 {
		t.Errorf("MatchedAmountRight = %d, want 300", s.MatchedAmountRight)
	}
	if s.UnmatchedAmountLeft != 0 {
		t.Errorf("UnmatchedAmountLeft = %d, want 0", s.UnmatchedAmountLeft)
	}
	if s.UnmatchedAmountRight != 0 {
		t.Errorf("UnmatchedAmountRight = %d, want 0", s.UnmatchedAmountRight)
	}
	if s.AmountDiffTotal != 0 {
		t.Errorf("AmountDiffTotal = %d, want 0", s.AmountDiffTotal)
	}
	if s.TotalDiscrepancy != 0 {
		t.Errorf("TotalDiscrepancy = %d, want 0", s.TotalDiscrepancy)
	}
}

func TestReconcile_MonetaryTotals_UnmatchedLeft(t *testing.T) {
	left := []Transaction{
		makeTx("l1", 100, "REF-1"),
		makeTx("l2", 250, "REF-ONLY-LEFT"),
	}
	right := []Transaction{
		makeTx("r1", 100, "REF-1"),
	}
	pair := config.Pair{DateWindow: "0d", AmountToleranceMinor: 0}

	res, err := Reconcile("p", "left", "right", left, right, pair)
	if err != nil {
		t.Fatal(err)
	}

	s := res.Summary
	if s.UnmatchedAmountLeft != 250 {
		t.Errorf("UnmatchedAmountLeft = %d, want 250", s.UnmatchedAmountLeft)
	}
	if s.TotalDiscrepancy != 250 {
		t.Errorf("TotalDiscrepancy = %d, want 250", s.TotalDiscrepancy)
	}
}

func TestReconcile_MonetaryTotals_UnmatchedRight(t *testing.T) {
	left := []Transaction{
		makeTx("l1", 100, "REF-1"),
	}
	right := []Transaction{
		makeTx("r1", 100, "REF-1"),
		makeTx("r2", 75, "REF-ONLY-RIGHT"),
	}
	pair := config.Pair{DateWindow: "0d", AmountToleranceMinor: 0}

	res, err := Reconcile("p", "left", "right", left, right, pair)
	if err != nil {
		t.Fatal(err)
	}

	s := res.Summary
	if s.UnmatchedAmountRight != 75 {
		t.Errorf("UnmatchedAmountRight = %d, want 75", s.UnmatchedAmountRight)
	}
	if s.TotalDiscrepancy != 75 {
		t.Errorf("TotalDiscrepancy = %d, want 75", s.TotalDiscrepancy)
	}
}

func TestReconcile_MonetaryTotals_AmountDiff(t *testing.T) {
	left := []Transaction{makeTx("l1", 100, "REF-1")}
	right := []Transaction{makeTx("r1", 103, "REF-1")}
	pair := config.Pair{DateWindow: "0d", AmountToleranceMinor: 0}

	res, err := Reconcile("p", "left", "right", left, right, pair)
	if err != nil {
		t.Fatal(err)
	}

	s := res.Summary
	if s.AmountDiffTotal != 3 {
		t.Errorf("AmountDiffTotal = %d, want 3 (abs diff)", s.AmountDiffTotal)
	}
	if s.TotalDiscrepancy != 3 {
		t.Errorf("TotalDiscrepancy = %d, want 3", s.TotalDiscrepancy)
	}
	// Amount diff transactions are not in unmatched
	if s.UnmatchedAmountLeft != 0 || s.UnmatchedAmountRight != 0 {
		t.Errorf("amount_diff should not contribute to unmatched totals")
	}
}

func TestReconcile_MonetaryTotals_TotalDiscrepancyInvariant(t *testing.T) {
	// TotalDiscrepancy = UnmatchedLeft + UnmatchedRight + AmountDiffTotal
	left := []Transaction{
		makeTx("l1", 100, "REF-MATCH"),
		makeTx("l2", 200, "REF-DIFF"),
		makeTx("l3", 50, "REF-L_ONLY"),
	}
	right := []Transaction{
		makeTx("r1", 100, "REF-MATCH"),
		makeTx("r2", 220, "REF-DIFF"), // diff = 20
		makeTx("r3", 80, "REF-RONLY"),
	}
	pair := config.Pair{DateWindow: "0d", AmountToleranceMinor: 0}

	res, err := Reconcile("p", "left", "right", left, right, pair)
	if err != nil {
		t.Fatal(err)
	}

	s := res.Summary
	computed := s.UnmatchedAmountLeft + s.UnmatchedAmountRight + s.AmountDiffTotal
	if s.TotalDiscrepancy != computed {
		t.Errorf("TotalDiscrepancy invariant broken: got %d, computed %d", s.TotalDiscrepancy, computed)
	}
	if s.AmountDiffTotal != 20 {
		t.Errorf("AmountDiffTotal = %d, want 20", s.AmountDiffTotal)
	}
	if s.UnmatchedAmountLeft != 50 {
		t.Errorf("UnmatchedAmountLeft = %d, want 50", s.UnmatchedAmountLeft)
	}
	if s.UnmatchedAmountRight != 80 {
		t.Errorf("UnmatchedAmountRight = %d, want 80", s.UnmatchedAmountRight)
	}
	if s.TotalDiscrepancy != 150 {
		t.Errorf("TotalDiscrepancy = %d, want 150", s.TotalDiscrepancy)
	}
}

func TestReconcile_MonetaryTotals_EmptyInputs(t *testing.T) {
	pair := config.Pair{DateWindow: "0d"}
	res, err := Reconcile("p", "left", "right", nil, nil, pair)
	if err != nil {
		t.Fatal(err)
	}
	s := res.Summary
	if s.MatchedAmountLeft != 0 || s.MatchedAmountRight != 0 ||
		s.UnmatchedAmountLeft != 0 || s.UnmatchedAmountRight != 0 ||
		s.AmountDiffTotal != 0 || s.TotalDiscrepancy != 0 {
		t.Errorf("expected all monetary totals to be 0 for empty inputs, got %+v", s)
	}
}

// makeTxGroup is like makeTx but also sets GroupKey, for duplicate-annotation tests.
func makeTxGroup(id string, amount int64, ref, group string) Transaction {
	tx := makeTx(id, amount, ref)
	tx.GroupKey = group
	return tx
}

// TestReconcile_DuplicatesAreNonGating reproduces the installment scenario: three
// rows share a GroupKey (invoice number) but have distinct References (transaction
// IDs) and amounts. All three must match normally AND be reported once as a
// duplicate group — never one or the other.
func TestReconcile_DuplicatesAreNonGating(t *testing.T) {
	left := []Transaction{
		makeTxGroup("l1", 100, "REF-1", "INV-1"),
		makeTxGroup("l2", 200, "REF-2", "INV-1"),
		makeTxGroup("l3", 300, "REF-3", "INV-1"),
	}
	right := []Transaction{
		makeTxGroup("r1", 100, "REF-1", "INV-1"),
		makeTxGroup("r2", 200, "REF-2", "INV-1"),
		makeTxGroup("r3", 300, "REF-3", "INV-1"),
	}
	pair := config.Pair{DateWindow: "0d", AmountToleranceMinor: 0}

	res, err := Reconcile("p", "left", "right", left, right, pair)
	if err != nil {
		t.Fatal(err)
	}

	if len(res.Matched) != 3 {
		t.Fatalf("Matched = %d, want 3 (all installments should match)", len(res.Matched))
	}
	if len(res.UnmatchedLeft) != 0 || len(res.UnmatchedRight) != 0 {
		t.Fatalf("expected no unmatched rows, got left=%d right=%d", len(res.UnmatchedLeft), len(res.UnmatchedRight))
	}

	// Duplicate annotation runs once per side, so we expect two groups (left + right),
	// each with all 3 transactions for GroupKey INV-1.
	if len(res.Duplicates) != 2 {
		t.Fatalf("Duplicates groups = %d, want 2 (one per side)", len(res.Duplicates))
	}
	for _, g := range res.Duplicates {
		if g.Reference != "INV-1" {
			t.Errorf("duplicate group key = %q, want INV-1", g.Reference)
		}
		if len(g.Transactions) != 3 {
			t.Errorf("duplicate group size = %d, want 3", len(g.Transactions))
		}
	}

	// A3: DuplicateCount counts transactions, not groups (2 groups but 6 transactions).
	if res.Summary.DuplicateCount != 6 {
		t.Errorf("DuplicateCount = %d, want 6 (transaction count, not group count)", res.Summary.DuplicateCount)
	}
}

// TestReconcile_BestCandidateNotFirstCandidate reproduces the scenario where a
// worse candidate appears before a better one in the right-side slice for the same
// reference: the exact match must win even though the amount-diff candidate comes first.
func TestReconcile_BestCandidateNotFirstCandidate(t *testing.T) {
	left := []Transaction{makeTx("l1", 100, "REF-1")}
	right := []Transaction{
		makeTx("r-diff", 150, "REF-1"),  // amount-diff candidate, listed first
		makeTx("r-exact", 100, "REF-1"), // exact candidate, listed second
	}
	pair := config.Pair{DateWindow: "0d", AmountToleranceMinor: 0}

	res, err := Reconcile("p", "left", "right", left, right, pair)
	if err != nil {
		t.Fatal(err)
	}

	if len(res.AmountDiff) != 0 {
		t.Fatalf("AmountDiff = %d, want 0 (exact match should have been preferred)", len(res.AmountDiff))
	}
	if len(res.Matched) != 1 {
		t.Fatalf("Matched = %d, want 1", len(res.Matched))
	}
	if res.Matched[0].Right.ID != "r-exact" {
		t.Errorf("matched right ID = %q, want %q", res.Matched[0].Right.ID, "r-exact")
	}
	if len(res.UnmatchedRight) != 1 || res.UnmatchedRight[0].ID != "r-diff" {
		t.Fatalf("expected r-diff to remain unmatched, got %+v", res.UnmatchedRight)
	}
}

// TestReconcileStreaming_BestCandidateNotFirstCandidate is the streaming-path
// equivalent of TestReconcile_BestCandidateNotFirstCandidate — the same two-pass
// best-candidate selection must apply in ReconcileStreaming's matching loop.
func TestReconcileStreaming_BestCandidateNotFirstCandidate(t *testing.T) {
	dir := t.TempDir()
	leftPath := filepath.Join(dir, "left.csv")
	rightPath := filepath.Join(dir, "right.csv")

	leftCSV := "id,date,amount,currency,reference,name\n" +
		"l1,2024-01-01,1.00,USD,REF-1,Left\n"
	rightCSV := "id,date,amount,currency,reference,name\n" +
		"r-diff,2024-01-01,1.50,USD,REF-1,Diff\n" +
		"r-exact,2024-01-01,1.00,USD,REF-1,Exact\n"

	if err := os.WriteFile(leftPath, []byte(leftCSV), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rightPath, []byte(rightCSV), 0600); err != nil {
		t.Fatal(err)
	}

	parserCfg := config.CSVParserCfg{
		Type: "csv", DateCol: "date", DateLayout: "2006-01-02", AmountCol: "amount",
		Decimal: ".", Multiplier: 100, CurrencyCol: "currency", RefCol: "reference", NameCol: "name",
	}
	pair := config.Pair{DateWindow: "0d", AmountToleranceMinor: 0}

	var out bytes.Buffer
	w := newJSONWriter(&out)
	w.SetMeta("p", "left", "right")
	idx := NewMemoryIndex()
	defer idx.Close()

	if err := ReconcileStreaming(
		context.Background(), "p", "left", "right", leftPath, rightPath,
		parserCfg, parserCfg, pair, idx, w, 0,
	); err != nil {
		t.Fatalf("ReconcileStreaming error: %v", err)
	}

	var res Result
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if len(res.AmountDiff) != 0 {
		t.Fatalf("AmountDiff = %d, want 0 (exact match should have been preferred)", len(res.AmountDiff))
	}
	if len(res.Matched) != 1 || res.Matched[0].Right.Name != "Exact" {
		t.Fatalf("expected exact match against the 'Exact' row, got %+v", res.Matched)
	}
}

// TestReconcile_ReconciledRatePct verifies the additive ReconciledRatePct field
// counts AmountDiff/TimingDiff outcomes alongside exact matches, while MatchRatePct
// (exact matches only) is unaffected.
func TestReconcile_ReconciledRatePct(t *testing.T) {
	left := []Transaction{
		makeTx("l1", 100, "REF-1"),
		makeTx("l2", 200, "REF-2"),
	}
	right := []Transaction{
		makeTx("r1", 150, "REF-1"), // amount diff (within no tolerance -> diff)
		makeTx("r2", 200, "REF-2"), // exact match
	}
	pair := config.Pair{DateWindow: "0d", AmountToleranceMinor: 0}

	res, err := Reconcile("p", "left", "right", left, right, pair)
	if err != nil {
		t.Fatal(err)
	}

	if res.Summary.MatchRatePct != 50 {
		t.Errorf("MatchRatePct = %v, want 50 (only the exact match counts)", res.Summary.MatchRatePct)
	}
	if res.Summary.ReconciledRatePct != 100 {
		t.Errorf("ReconciledRatePct = %v, want 100 (match + amount_diff)", res.Summary.ReconciledRatePct)
	}
}

// TestReconcile_NameMatchThreshold verifies that the configured threshold (rather
// than a hardcoded 0.5) governs whether a token-mode candidate counts as a match.
func TestReconcile_NameMatchThreshold(t *testing.T) {
	// "Acme Corp Payment" vs "Acme Corp Invoice" share 2 of 4 total unique tokens
	// (acme, corp) -> Jaccard = 2/4 = 0.5. With the default threshold (>0.5),
	// this does not match. With a lower configured threshold, it does.
	left := []Transaction{{
		ID: "l1", Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Amount: 100, Currency: "USD", Name: "Acme Corp Payment", Source: "test",
	}}
	right := []Transaction{{
		ID: "r1", Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Amount: 100, Currency: "USD", Name: "Acme Corp Invoice", Source: "test",
	}}

	strict := config.Pair{DateWindow: "0d", AmountToleranceMinor: 0, NameMode: "tokens"}
	res, err := Reconcile("p", "left", "right", left, right, strict)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Matched) != 0 {
		t.Fatalf("default threshold: Matched = %d, want 0 (score 0.5 is not > 0.5)", len(res.Matched))
	}

	lenient := config.Pair{DateWindow: "0d", AmountToleranceMinor: 0, NameMode: "tokens", NameMatchThreshold: 0.4}
	res, err = Reconcile("p", "left", "right", left, right, lenient)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Matched) != 1 {
		t.Fatalf("lenient threshold: Matched = %d, want 1 (score 0.5 > 0.4)", len(res.Matched))
	}
}

// TestReconcile_EmptyCurrencyWarning verifies that rows with an empty currency,
// mixed with a non-empty base currency, are still included in monetary totals
// (no behavior change) but surface a warning rather than being silently absorbed.
func TestReconcile_EmptyCurrencyWarning(t *testing.T) {
	left := []Transaction{makeTx("l1", 100, "REF-1")}
	right := []Transaction{makeTx("r1", 100, "REF-1")}
	right[0].Currency = "" // empty currency mixed with left's USD

	pair := config.Pair{DateWindow: "0d", AmountToleranceMinor: 0}
	res, err := Reconcile("p", "left", "right", left, right, pair)
	if err != nil {
		t.Fatal(err)
	}

	// Behavior unchanged: the empty-currency row still counts toward totals.
	if res.Summary.MatchedAmountRight != 100 {
		t.Errorf("MatchedAmountRight = %d, want 100 (empty currency rows still count)", res.Summary.MatchedAmountRight)
	}
	if len(res.Warnings) == 0 {
		t.Fatal("expected a warning about the empty-currency row, got none")
	}
	if !strings.Contains(res.Warnings[0], "empty currency") {
		t.Errorf("warning = %q, want it to mention empty currency", res.Warnings[0])
	}
}

// TestReconcile_DuplicatePolicy_Flag is the baseline: same as existing non-gating
// behavior — duplicates are reported and all rows participate in matching.
func TestReconcile_DuplicatePolicy_Flag(t *testing.T) {
	left := []Transaction{
		makeTxGroup("l1", 100, "REF-1", "INV-1"),
		makeTxGroup("l2", 200, "REF-2", "INV-1"),
	}
	right := []Transaction{
		makeTxGroup("r1", 100, "REF-1", "INV-1"),
		makeTxGroup("r2", 200, "REF-2", "INV-1"),
	}
	pair := config.Pair{DateWindow: "0d"}
	opts := ReconcileOptions{
		LeftPolicy:  config.DuplicatePolicyFlag,
		RightPolicy: config.DuplicatePolicyFlag,
	}
	res, err := Reconcile("p", "left", "right", left, right, pair, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Matched) != 2 {
		t.Errorf("Matched = %d, want 2", len(res.Matched))
	}
	if len(res.Duplicates) != 2 { // one group per side
		t.Errorf("Duplicates = %d, want 2 groups (one per side)", len(res.Duplicates))
	}
	if res.Summary.DuplicateCount != 4 { // 2 txns per side
		t.Errorf("DuplicateCount = %d, want 4", res.Summary.DuplicateCount)
	}
}

// TestReconcile_DuplicatePolicy_Keep: all rows participate in matching but no
// duplicate events are emitted.
func TestReconcile_DuplicatePolicy_Keep(t *testing.T) {
	left := []Transaction{
		makeTxGroup("l1", 100, "REF-1", "INV-1"),
		makeTxGroup("l2", 200, "REF-2", "INV-1"),
	}
	right := []Transaction{
		makeTxGroup("r1", 100, "REF-1", "INV-1"),
		makeTxGroup("r2", 200, "REF-2", "INV-1"),
	}
	pair := config.Pair{DateWindow: "0d"}
	opts := ReconcileOptions{
		LeftPolicy:  config.DuplicatePolicyKeep,
		RightPolicy: config.DuplicatePolicyKeep,
	}
	res, err := Reconcile("p", "left", "right", left, right, pair, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Matched) != 2 {
		t.Errorf("Matched = %d, want 2 (all rows still participate)", len(res.Matched))
	}
	if len(res.Duplicates) != 0 {
		t.Errorf("Duplicates = %d, want 0 (keep suppresses reporting)", len(res.Duplicates))
	}
	if res.Summary.DuplicateCount != 0 {
		t.Errorf("DuplicateCount = %d, want 0", res.Summary.DuplicateCount)
	}
}

// TestReconcile_DuplicatePolicy_Merge_LeftSide: only the first-seen left row per
// GroupKey participates in matching.
func TestReconcile_DuplicatePolicy_Merge_LeftSide(t *testing.T) {
	// l1 and l2 share a GroupKey; only l1 (first) should reach matching.
	// Right side has no GroupKey → no right-side duplicate events regardless of policy.
	left := []Transaction{
		makeTxGroup("l1", 100, "REF-1", "INV-1"),
		makeTxGroup("l2", 200, "REF-2", "INV-1"),
	}
	right := []Transaction{
		makeTx("r1", 100, "REF-1"),
		makeTx("r2", 200, "REF-2"),
	}
	pair := config.Pair{DateWindow: "0d"}
	opts := ReconcileOptions{LeftPolicy: config.DuplicatePolicyMerge}
	res, err := Reconcile("p", "left", "right", left, right, pair, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Matched) != 1 {
		t.Errorf("Matched = %d, want 1 (only first-seen left row)", len(res.Matched))
	}
	if res.Matched[0].Left.ID != "l1" {
		t.Errorf("matched left ID = %q, want l1", res.Matched[0].Left.ID)
	}
	if len(res.Duplicates) != 0 {
		t.Errorf("Duplicates = %d, want 0 (merge suppresses reporting)", len(res.Duplicates))
	}
}

// TestReconcile_DuplicatePolicy_Latest_LeftSide: only the last-seen left row per
// GroupKey participates in matching.
func TestReconcile_DuplicatePolicy_Latest_LeftSide(t *testing.T) {
	// l1 and l2 share a GroupKey; only l2 (last) should reach matching.
	// Right side has no GroupKey → no right-side duplicate events.
	left := []Transaction{
		makeTxGroup("l1", 100, "REF-1", "INV-1"),
		makeTxGroup("l2", 200, "REF-2", "INV-1"),
	}
	right := []Transaction{
		makeTx("r1", 100, "REF-1"),
		makeTx("r2", 200, "REF-2"),
	}
	pair := config.Pair{DateWindow: "0d"}
	opts := ReconcileOptions{LeftPolicy: config.DuplicatePolicyLatest}
	res, err := Reconcile("p", "left", "right", left, right, pair, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Matched) != 1 {
		t.Errorf("Matched = %d, want 1 (only last-seen left row)", len(res.Matched))
	}
	if res.Matched[0].Left.ID != "l2" {
		t.Errorf("matched left ID = %q, want l2", res.Matched[0].Left.ID)
	}
	if len(res.Duplicates) != 0 {
		t.Errorf("Duplicates = %d, want 0 (latest suppresses reporting)", len(res.Duplicates))
	}
}

// TestReconcile_DuplicatePolicy_Merge_RightSide: only the first-seen right row per
// GroupKey is in the index; duplicates do not create a second match opportunity.
func TestReconcile_DuplicatePolicy_Merge_RightSide(t *testing.T) {
	left := []Transaction{
		makeTxGroup("l1", 100, "REF-1", "INV-1"),
	}
	// r1 and r2 share GroupKey; only r1 (first-seen) should be in index.
	right := []Transaction{
		makeTxGroup("r1", 100, "REF-1", "INV-1"),
		makeTxGroup("r2", 100, "REF-1", "INV-1"), // duplicate: same ref, same group
	}
	pair := config.Pair{DateWindow: "0d"}
	opts := ReconcileOptions{RightPolicy: config.DuplicatePolicyMerge}
	res, err := Reconcile("p", "left", "right", left, right, pair, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Matched) != 1 {
		t.Errorf("Matched = %d, want 1", len(res.Matched))
	}
	// r2 was discarded before matching, so no unmatched right rows.
	if len(res.UnmatchedRight) != 0 {
		t.Errorf("UnmatchedRight = %d, want 0 (r2 was discarded by merge)", len(res.UnmatchedRight))
	}
	if len(res.Duplicates) != 0 {
		t.Errorf("Duplicates = %d, want 0", len(res.Duplicates))
	}
}

// TestReconcile_DuplicatePolicy_Latest_RightSide: the last-seen right row per
// GroupKey wins; the first is discarded.
func TestReconcile_DuplicatePolicy_Latest_RightSide(t *testing.T) {
	left := []Transaction{
		makeTxGroup("l1", 200, "REF-1", "INV-1"),
	}
	// r1 (amount=100) and r2 (amount=200) share GroupKey; latest (r2) should win.
	right := []Transaction{
		makeTxGroup("r1", 100, "REF-1", "INV-1"),
		makeTxGroup("r2", 200, "REF-1", "INV-1"),
	}
	pair := config.Pair{DateWindow: "0d"}
	opts := ReconcileOptions{RightPolicy: config.DuplicatePolicyLatest}
	res, err := Reconcile("p", "left", "right", left, right, pair, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Matched) != 1 {
		t.Errorf("Matched = %d, want 1 (l1 matches r2 exactly)", len(res.Matched))
	}
	if len(res.AmountDiff) != 0 {
		t.Errorf("AmountDiff = %d, want 0 (r2 amount matches l1)", len(res.AmountDiff))
	}
}
