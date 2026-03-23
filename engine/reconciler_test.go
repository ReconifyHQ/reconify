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
