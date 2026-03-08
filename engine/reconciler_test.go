package engine

import (
	"testing"
	"time"

	"github.com/reconify/reconify/config"
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
		makeTx("l3", 50, "REF-LONLY"),
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
