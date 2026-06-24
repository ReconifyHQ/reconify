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

// ---------------------------------------------------------------------------
// Invariant helpers — called at the end of every scenario.
// ---------------------------------------------------------------------------

// assertRowCoverage verifies every input row ID appears exactly once across all
// output buckets — including grouped and ambiguous slices from the one_to_many pass.
func assertRowCoverage(t *testing.T, left, right []Transaction, res *Result) {
	t.Helper()

	wantLeft := make(map[string]bool, len(left))
	for _, tx := range left {
		wantLeft[tx.ID] = true
	}
	wantRight := make(map[string]bool, len(right))
	for _, tx := range right {
		wantRight[tx.ID] = true
	}

	gotLeft := make(map[string]bool)
	gotRight := make(map[string]bool)
	for _, p := range res.Matched {
		gotLeft[p.Left.ID] = true
		gotRight[p.Right.ID] = true
	}
	for _, p := range res.AmountDiff {
		gotLeft[p.Left.ID] = true
		gotRight[p.Right.ID] = true
	}
	for _, p := range res.TimingDiff {
		gotLeft[p.Left.ID] = true
		gotRight[p.Right.ID] = true
	}
	for _, tx := range res.UnmatchedLeft {
		gotLeft[tx.ID] = true
	}
	for _, tx := range res.UnmatchedRight {
		gotRight[tx.ID] = true
	}
	// one_to_many grouped outcomes
	for _, gm := range res.GroupedMatched {
		gotLeft[gm.Left.ID] = true
		for _, r := range gm.Rights {
			gotRight[r.ID] = true
		}
	}
	for _, gd := range res.GroupedAmountDiff {
		gotLeft[gd.Left.ID] = true
		for _, r := range gd.Rights {
			gotRight[r.ID] = true
		}
	}
	for _, gt := range res.GroupedTimingDiff {
		gotLeft[gt.Left.ID] = true
		for _, r := range gt.Rights {
			gotRight[r.ID] = true
		}
	}
	for _, ag := range res.AmbiguousGroups {
		for _, tx := range ag.LeftRows {
			gotLeft[tx.ID] = true
		}
		for _, r := range ag.Rights {
			gotRight[r.ID] = true
		}
	}

	for id := range wantLeft {
		if !gotLeft[id] {
			t.Errorf("assertRowCoverage: left row %q not accounted for in output", id)
		}
	}
	for id := range gotLeft {
		if !wantLeft[id] {
			t.Errorf("assertRowCoverage: left row %q in output but not in input", id)
		}
	}
	for id := range wantRight {
		if !gotRight[id] {
			t.Errorf("assertRowCoverage: right row %q not accounted for in output", id)
		}
	}
	for id := range gotRight {
		if !wantRight[id] {
			t.Errorf("assertRowCoverage: right row %q in output but not in input", id)
		}
	}
}

// assertMonetaryInvariant verifies the extended monetary invariant:
// TotalDiscrepancy == UnmatchedAmountLeft + UnmatchedAmountRight + AmountDiffTotal
//
//   - AmbiguousAmountLeft + AmbiguousAmountRight
func assertMonetaryInvariant(t *testing.T, s Summary) {
	t.Helper()
	computed := s.UnmatchedAmountLeft + s.UnmatchedAmountRight + s.AmountDiffTotal +
		s.AmbiguousAmountLeft + s.AmbiguousAmountRight
	if s.TotalDiscrepancy != computed {
		t.Errorf("monetary invariant: TotalDiscrepancy=%d want %d (UL=%d + UR=%d + AD=%d + AL=%d + AR=%d)",
			s.TotalDiscrepancy, computed,
			s.UnmatchedAmountLeft, s.UnmatchedAmountRight, s.AmountDiffTotal,
			s.AmbiguousAmountLeft, s.AmbiguousAmountRight)
	}
}

// assertSummaryMatchesCounts verifies that Summary count fields agree with the
// actual slice lengths in the Result, including grouped/ambiguous slices.
func assertSummaryMatchesCounts(t *testing.T, res *Result) {
	t.Helper()
	if res.Summary.MatchedCount != len(res.Matched) {
		t.Errorf("Summary.MatchedCount=%d len(Matched)=%d", res.Summary.MatchedCount, len(res.Matched))
	}
	if res.Summary.UnmatchedLeft != len(res.UnmatchedLeft) {
		t.Errorf("Summary.UnmatchedLeft=%d len(UnmatchedLeft)=%d", res.Summary.UnmatchedLeft, len(res.UnmatchedLeft))
	}
	if res.Summary.UnmatchedRight != len(res.UnmatchedRight) {
		t.Errorf("Summary.UnmatchedRight=%d len(UnmatchedRight)=%d", res.Summary.UnmatchedRight, len(res.UnmatchedRight))
	}
	if res.Summary.AmountDiffCount != len(res.AmountDiff) {
		t.Errorf("Summary.AmountDiffCount=%d len(AmountDiff)=%d", res.Summary.AmountDiffCount, len(res.AmountDiff))
	}
	if res.Summary.TimingDiffCount != len(res.TimingDiff) {
		t.Errorf("Summary.TimingDiffCount=%d len(TimingDiff)=%d", res.Summary.TimingDiffCount, len(res.TimingDiff))
	}
	if res.Summary.GroupedMatchedCount != len(res.GroupedMatched) {
		t.Errorf("Summary.GroupedMatchedCount=%d len(GroupedMatched)=%d", res.Summary.GroupedMatchedCount, len(res.GroupedMatched))
	}
	if res.Summary.GroupedAmountDiffCount != len(res.GroupedAmountDiff) {
		t.Errorf("Summary.GroupedAmountDiffCount=%d len(GroupedAmountDiff)=%d", res.Summary.GroupedAmountDiffCount, len(res.GroupedAmountDiff))
	}
	if res.Summary.GroupedTimingDiffCount != len(res.GroupedTimingDiff) {
		t.Errorf("Summary.GroupedTimingDiffCount=%d len(GroupedTimingDiff)=%d", res.Summary.GroupedTimingDiffCount, len(res.GroupedTimingDiff))
	}
	if res.Summary.AmbiguousGroupCount != len(res.AmbiguousGroups) {
		t.Errorf("Summary.AmbiguousGroupCount=%d len(AmbiguousGroups)=%d", res.Summary.AmbiguousGroupCount, len(res.AmbiguousGroups))
	}
}

func TestPasses_UnsupportedPassType_ReturnsError(t *testing.T) {
	left := []Transaction{makeTx("l1", 100, "REF-1")}
	right := []Transaction{makeTx("r1", 100, "REF-1")}
	pair := config.Pair{
		DateWindow:           "0d",
		AmountToleranceMinor: 0,
		Passes: []config.PassConfig{
			{Type: "unknown_type"},
		},
	}

	_, err := Reconcile("p", "left", "right", left, right, pair)
	if err == nil {
		t.Fatal("expected unsupported pass type error")
	}
	if !strings.Contains(err.Error(), `unsupported pass type "unknown_type"`) {
		t.Fatalf("expected unsupported pass type error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func makeTxNamed(id string, amount int64, ref, name string) Transaction {
	tx := makeTx(id, amount, ref)
	tx.Name = name
	return tx
}

func makeTxFull(id string, amount int64, ref, name string, date time.Time) Transaction {
	return Transaction{
		ID:        id,
		Date:      date,
		Amount:    amount,
		Currency:  "USD",
		Reference: ref,
		Name:      name,
		Source:    "test",
	}
}

// ---------------------------------------------------------------------------
// Test A — name_tokens_one_to_one only: reference matching must NOT run
//
// FAILING ACCEPTANCE TEST: today the engine ignores Passes and runs reference
// matching unconditionally (NameMode="" means no name pass either), so
// MatchedCount will be 3 (ref matches only) instead of 5 (all name matches).
// ---------------------------------------------------------------------------
func TestPasses_NameTokensOnly_SkipsReferenceMatching(t *testing.T) {
	// 3 rows: both reference and name match available. With name-only pass the
	// engine must use name matching and ignore the references.
	// 2 rows: no reference, matching names only.
	// 2 rows: left only, no right counterpart, no name match available.
	left := []Transaction{
		makeTxNamed("l1", 100, "REF-1", "Acme Corp"),
		makeTxNamed("l2", 100, "REF-2", "Global Pay"),
		makeTxNamed("l3", 100, "REF-3", "Bank Wire"),
		makeTxNamed("l4", 100, "", "Stripe Fee"),
		makeTxNamed("l5", 100, "", "Paypal Transfer"),
		makeTxNamed("l6", 100, "REF-6", "Ghost Transaction"),
		makeTxNamed("l7", 100, "REF-7", "Phantom Transaction"),
	}
	right := []Transaction{
		makeTxNamed("r1", 100, "REF-1", "Acme Corp"),
		makeTxNamed("r2", 100, "REF-2", "Global Pay"),
		makeTxNamed("r3", 100, "REF-3", "Bank Wire"),
		makeTxNamed("r4", 100, "", "Stripe Fee"),
		makeTxNamed("r5", 100, "", "Paypal Transfer"),
		// No counterpart for l6/l7; their name values do not appear on the right.
	}
	pair := config.Pair{
		DateWindow:           "0d",
		AmountToleranceMinor: 0,
		Passes:               []config.PassConfig{{Type: config.PassTypeNameTokensOneToOne}},
	}

	res, err := Reconcile("p", "left", "right", left, right, pair)
	if err != nil {
		t.Fatal(err)
	}

	if res.Summary.MatchedCount != 5 {
		t.Errorf("MatchedCount=%d want 5 (all name-eligible rows matched by name pass)",
			res.Summary.MatchedCount)
	}
	if res.Summary.UnmatchedLeft != 2 {
		t.Errorf("UnmatchedLeft=%d want 2 (l6 and l7 have no name match on the right)",
			res.Summary.UnmatchedLeft)
	}
	if res.Summary.UnmatchedRight != 0 {
		t.Errorf("UnmatchedRight=%d want 0 (all 5 right rows consumed by name pass)",
			res.Summary.UnmatchedRight)
	}
	assertRowCoverage(t, left, right, res)
	assertMonetaryInvariant(t, res.Summary)
	assertSummaryMatchesCounts(t, res)
}

// ---------------------------------------------------------------------------
// Test B — [reference_one_to_one, name_tokens_one_to_one] via Passes, no NameMode
//
// FAILING ACCEPTANCE TEST: today the engine ignores Passes; NameMode="" means
// the name pass never runs, so MatchedCount=4 (ref only) instead of 7.
// ---------------------------------------------------------------------------
func TestPasses_RefThenName_ViaPassesNotNameMode(t *testing.T) {
	left := []Transaction{
		makeTxNamed("l1", 100, "REF-1", "Ref Match One"),
		makeTxNamed("l2", 100, "REF-2", "Ref Match Two"),
		makeTxNamed("l3", 100, "REF-3", "Ref Match Three"),
		makeTxNamed("l4", 100, "REF-4", "Ref Match Four"),
		makeTxNamed("l5", 100, "", "Name Only Alpha"),
		makeTxNamed("l6", 100, "", "Name Only Beta"),
		makeTxNamed("l7", 100, "", "Name Only Gamma"),
	}
	right := []Transaction{
		makeTxNamed("r1", 100, "REF-1", "Ref Match One"),
		makeTxNamed("r2", 100, "REF-2", "Ref Match Two"),
		makeTxNamed("r3", 100, "REF-3", "Ref Match Three"),
		makeTxNamed("r4", 100, "REF-4", "Ref Match Four"),
		makeTxNamed("r5", 100, "", "Name Only Alpha"),
		makeTxNamed("r6", 100, "", "Name Only Beta"),
		makeTxNamed("r7", 100, "", "Name Only Gamma"),
	}
	pair := config.Pair{
		DateWindow:           "0d",
		AmountToleranceMinor: 0,
		Passes: []config.PassConfig{
			{Type: config.PassTypeReferenceOneToOne},
			{Type: config.PassTypeNameTokensOneToOne},
		},
	}

	res, err := Reconcile("p", "left", "right", left, right, pair)
	if err != nil {
		t.Fatal(err)
	}

	if res.Summary.MatchedCount != 7 {
		t.Errorf("MatchedCount=%d want 7 (4 ref + 3 name)", res.Summary.MatchedCount)
	}
	if res.Summary.UnmatchedLeft != 0 {
		t.Errorf("UnmatchedLeft=%d want 0", res.Summary.UnmatchedLeft)
	}
	if res.Summary.UnmatchedRight != 0 {
		t.Errorf("UnmatchedRight=%d want 0", res.Summary.UnmatchedRight)
	}

	// Ref-matched rows (l1–l4) must not appear in any unmatched output — they
	// must have been consumed in pass 1 and not re-processed in pass 2.
	unmatchedLeftIDs := make(map[string]bool)
	for _, tx := range res.UnmatchedLeft {
		unmatchedLeftIDs[tx.ID] = true
	}
	for _, id := range []string{"l1", "l2", "l3", "l4"} {
		if unmatchedLeftIDs[id] {
			t.Errorf("ref-matched row %q in UnmatchedLeft: must have been consumed in pass 1", id)
		}
	}

	assertRowCoverage(t, left, right, res)
	assertMonetaryInvariant(t, res.Summary)
	assertSummaryMatchesCounts(t, res)
}

// ---------------------------------------------------------------------------
// Test C — pass order determines which right row wins
//
// FAILING ACCEPTANCE TEST: today the engine ignores Passes and always runs
// reference first, so both orderings produce the same result (r-ref wins).
// After dispatch, [ref, name] picks r-ref and [name, ref] picks r-name.
// ---------------------------------------------------------------------------
func TestPasses_OrderDeterminesWinner(t *testing.T) {
	date := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	// l1 has both a reference match (r-ref) and a name match (r-name) available.
	// r-ref shares the reference with l1 but a different name.
	// r-name shares the name with l1 but a different reference.
	l1 := makeTxFull("l1", 100, "REF-1", "Shared Vendor", date)
	rRef := makeTxFull("r-ref", 100, "REF-1", "Different Name", date)
	rName := makeTxFull("r-name", 100, "OTHER-REF", "Shared Vendor", date)

	left := []Transaction{l1}
	right := []Transaction{rRef, rName}

	// Run 1: reference first — r-ref must win; r-name stays unmatched.
	refFirst := config.Pair{
		DateWindow: "0d", AmountToleranceMinor: 0,
		Passes: []config.PassConfig{
			{Type: config.PassTypeReferenceOneToOne},
			{Type: config.PassTypeNameTokensOneToOne},
		},
	}
	res1, err := Reconcile("p", "left", "right", left, right, refFirst)
	if err != nil {
		t.Fatal(err)
	}
	if len(res1.Matched) != 1 || res1.Matched[0].Right.ID != "r-ref" {
		t.Errorf("ref-first: expected r-ref to win, got Matched=%+v", res1.Matched)
	}
	if len(res1.UnmatchedRight) != 1 || res1.UnmatchedRight[0].ID != "r-name" {
		t.Errorf("ref-first: expected r-name unmatched, got %+v", res1.UnmatchedRight)
	}
	assertRowCoverage(t, left, right, res1)

	// Run 2: name first — r-name must win; r-ref stays unmatched.
	nameFirst := config.Pair{
		DateWindow: "0d", AmountToleranceMinor: 0,
		Passes: []config.PassConfig{
			{Type: config.PassTypeNameTokensOneToOne},
			{Type: config.PassTypeReferenceOneToOne},
		},
	}
	res2, err := Reconcile("p", "left", "right", left, right, nameFirst)
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.Matched) != 1 || res2.Matched[0].Right.ID != "r-name" {
		t.Errorf("name-first: expected r-name to win, got Matched=%+v", res2.Matched)
	}
	if len(res2.UnmatchedRight) != 1 || res2.UnmatchedRight[0].ID != "r-ref" {
		t.Errorf("name-first: expected r-ref unmatched, got %+v", res2.UnmatchedRight)
	}
	assertRowCoverage(t, left, right, res2)
}

// ---------------------------------------------------------------------------
// Test D — AmountDiff/TimingDiff semantics within a pass pipeline (corrected)
//
// Semantics from reconciler.go:
//
//	amount within tolerance AND date within window  → Matched
//	amount outside tolerance AND date within window → AmountDiff
//	amount within tolerance AND date outside window → TimingDiff
//	both outside tolerance/window                   → Unmatched (no classification)
//
// FAILING ACCEPTANCE TEST: today the name pass never runs (NameMode=""),
// so MatchedCount=2 and UnmatchedLeft=2 instead of MatchedCount=4 and
// UnmatchedLeft=0.
// ---------------------------------------------------------------------------
func TestPasses_CorrectAmountDiffTimingDiffSemantics(t *testing.T) {
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	late := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC) // 14 days — outside 5d window

	left := []Transaction{
		// Group 1: within tolerance (5) and date ok → Matched
		makeTxFull("l1", 100, "REF-1", "Vendor One", base),
		makeTxFull("l2", 200, "REF-2", "Vendor Two", base),
		// Group 2: amount outside tolerance, date ok → AmountDiff
		makeTxFull("l3", 100, "REF-3", "Vendor Three", base),
		makeTxFull("l4", 100, "REF-4", "Vendor Four", base),
		// Group 3: amount ok, date outside window → TimingDiff
		makeTxFull("l5", 500, "REF-5", "Vendor Five", base),
		makeTxFull("l6", 600, "REF-6", "Vendor Six", base),
		// Group 4: both amount and date fail → Unmatched from ref pass.
		// Name pass picks these up via r7-name/r8-name.
		makeTxFull("l7", 100, "REF-7", "Vendor Seven", base),
		makeTxFull("l8", 100, "REF-8", "Vendor Eight", base),
	}
	right := []Transaction{
		// Group 1: amount diff within tolerance=5
		makeTxFull("r1", 103, "REF-1", "Vendor One", base), // diff=3 ≤ 5 → Matched
		makeTxFull("r2", 204, "REF-2", "Vendor Two", base), // diff=4 ≤ 5 → Matched
		// Group 2: amount diff outside tolerance
		makeTxFull("r3", 120, "REF-3", "Vendor Three", base), // diff=20 → AmountDiff
		makeTxFull("r4", 130, "REF-4", "Vendor Four", base),  // diff=30 → AmountDiff
		// Group 3: date 14 days off, amount exact → TimingDiff
		makeTxFull("r5", 500, "REF-5", "Vendor Five", late),
		makeTxFull("r6", 600, "REF-6", "Vendor Six", late),
		// Group 4: both amount and date fail (diff=100 and 14d) → Unmatched.
		// Empty name so name pass skips these right rows.
		makeTxFull("r7", 200, "REF-7", "", late),
		makeTxFull("r8", 300, "REF-8", "", late),
		// Group 5: name-only right rows for the name pass to consume l7/l8.
		makeTxFull("r7-name", 100, "", "Vendor Seven", base),
		makeTxFull("r8-name", 100, "", "Vendor Eight", base),
	}
	pair := config.Pair{
		DateWindow:           "5d",
		AmountToleranceMinor: 5,
		Passes: []config.PassConfig{
			{Type: config.PassTypeReferenceOneToOne},
			{Type: config.PassTypeNameTokensOneToOne},
		},
	}

	res, err := Reconcile("p", "left", "right", left, right, pair)
	if err != nil {
		t.Fatal(err)
	}

	// 2 from group 1 (within tolerance) + 2 from name pass (l7/l8 via r7-name/r8-name)
	if res.Summary.MatchedCount != 4 {
		t.Errorf("MatchedCount=%d want 4 (2 within-tolerance + 2 name-pass)", res.Summary.MatchedCount)
	}
	if res.Summary.AmountDiffCount != 2 {
		t.Errorf("AmountDiffCount=%d want 2", res.Summary.AmountDiffCount)
	}
	if res.Summary.TimingDiffCount != 2 {
		t.Errorf("TimingDiffCount=%d want 2", res.Summary.TimingDiffCount)
	}
	if res.Summary.UnmatchedLeft != 0 {
		t.Errorf("UnmatchedLeft=%d want 0 (all left rows classified)", res.Summary.UnmatchedLeft)
	}
	// r7 and r8 stay unmatched — they had empty names so name pass skipped them.
	if res.Summary.UnmatchedRight != 2 {
		t.Errorf("UnmatchedRight=%d want 2 (r7 and r8)", res.Summary.UnmatchedRight)
	}
	assertRowCoverage(t, left, right, res)
	assertMonetaryInvariant(t, res.Summary)
	assertSummaryMatchesCounts(t, res)
}

// ---------------------------------------------------------------------------
// Test E — legacy NameMode="tokens" path must be unchanged (regression guard)
//
// This test uses the same dataset as Test D but configures via NameMode="tokens"
// (no Passes). It must pass BOTH before and after the dispatch implementation
// because it exercises the existing code path.
// ---------------------------------------------------------------------------
func TestPasses_LegacyNameModeTokens_Unchanged(t *testing.T) {
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	late := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	left := []Transaction{
		makeTxFull("l1", 100, "REF-1", "Vendor One", base),
		makeTxFull("l2", 200, "REF-2", "Vendor Two", base),
		makeTxFull("l3", 100, "REF-3", "Vendor Three", base),
		makeTxFull("l4", 100, "REF-4", "Vendor Four", base),
		makeTxFull("l5", 500, "REF-5", "Vendor Five", base),
		makeTxFull("l6", 600, "REF-6", "Vendor Six", base),
		makeTxFull("l7", 100, "REF-7", "Vendor Seven", base),
		makeTxFull("l8", 100, "REF-8", "Vendor Eight", base),
	}
	right := []Transaction{
		makeTxFull("r1", 103, "REF-1", "Vendor One", base),
		makeTxFull("r2", 204, "REF-2", "Vendor Two", base),
		makeTxFull("r3", 120, "REF-3", "Vendor Three", base),
		makeTxFull("r4", 130, "REF-4", "Vendor Four", base),
		makeTxFull("r5", 500, "REF-5", "Vendor Five", late),
		makeTxFull("r6", 600, "REF-6", "Vendor Six", late),
		makeTxFull("r7", 200, "REF-7", "", late),
		makeTxFull("r8", 300, "REF-8", "", late),
		makeTxFull("r7-name", 100, "", "Vendor Seven", base),
		makeTxFull("r8-name", 100, "", "Vendor Eight", base),
	}
	pair := config.Pair{
		DateWindow:           "5d",
		AmountToleranceMinor: 5,
		NameMode:             "tokens",
	}

	res, err := Reconcile("p", "left", "right", left, right, pair)
	if err != nil {
		t.Fatal(err)
	}

	if res.Summary.MatchedCount != 4 {
		t.Errorf("MatchedCount=%d want 4", res.Summary.MatchedCount)
	}
	if res.Summary.AmountDiffCount != 2 {
		t.Errorf("AmountDiffCount=%d want 2", res.Summary.AmountDiffCount)
	}
	if res.Summary.TimingDiffCount != 2 {
		t.Errorf("TimingDiffCount=%d want 2", res.Summary.TimingDiffCount)
	}
	if res.Summary.UnmatchedLeft != 0 {
		t.Errorf("UnmatchedLeft=%d want 0", res.Summary.UnmatchedLeft)
	}
	if res.Summary.UnmatchedRight != 2 {
		t.Errorf("UnmatchedRight=%d want 2", res.Summary.UnmatchedRight)
	}
	assertRowCoverage(t, left, right, res)
	assertMonetaryInvariant(t, res.Summary)
	assertSummaryMatchesCounts(t, res)
}

// ---------------------------------------------------------------------------
// Test F — batch, streaming-memory, and streaming-disk must agree on explicit passes
//
// FAILING ACCEPTANCE TEST: the streaming path reads pair.NameMode, not Passes.
// With Passes=[ref,name] and NameMode="", the name pass never runs in streaming,
// so the streaming assertions produce MatchedCount=4 instead of 7.
// ---------------------------------------------------------------------------
func TestPasses_StreamingParity_ExplicitPasses(t *testing.T) {
	// Identical dataset to Test B (4 ref + 3 name rows) written to CSV.
	dir := t.TempDir()
	leftPath := filepath.Join(dir, "left.csv")
	rightPath := filepath.Join(dir, "right.csv")

	leftCSV := "id,date,amount,currency,reference,name\n" +
		"l1,2024-01-01,1.00,USD,REF-1,Ref Match One\n" +
		"l2,2024-01-01,1.00,USD,REF-2,Ref Match Two\n" +
		"l3,2024-01-01,1.00,USD,REF-3,Ref Match Three\n" +
		"l4,2024-01-01,1.00,USD,REF-4,Ref Match Four\n" +
		"l5,2024-01-01,1.00,USD,,Name Only Alpha\n" +
		"l6,2024-01-01,1.00,USD,,Name Only Beta\n" +
		"l7,2024-01-01,1.00,USD,,Name Only Gamma\n"

	rightCSV := "id,date,amount,currency,reference,name\n" +
		"r1,2024-01-01,1.00,USD,REF-1,Ref Match One\n" +
		"r2,2024-01-01,1.00,USD,REF-2,Ref Match Two\n" +
		"r3,2024-01-01,1.00,USD,REF-3,Ref Match Three\n" +
		"r4,2024-01-01,1.00,USD,REF-4,Ref Match Four\n" +
		"r5,2024-01-01,1.00,USD,,Name Only Alpha\n" +
		"r6,2024-01-01,1.00,USD,,Name Only Beta\n" +
		"r7,2024-01-01,1.00,USD,,Name Only Gamma\n"

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
		Multiplier:  100,
		CurrencyCol: "currency",
		RefCol:      "reference",
		NameCol:     "name",
	}
	pair := config.Pair{
		DateWindow:           "0d",
		AmountToleranceMinor: 0,
		Passes: []config.PassConfig{
			{Type: config.PassTypeReferenceOneToOne},
			{Type: config.PassTypeNameTokensOneToOne},
		},
	}

	wantMatched := 7
	wantUnmatchedLeft := 0
	wantUnmatchedRight := 0

	// --- Batch (in-memory) path ---
	left := []Transaction{
		makeTxNamed("l1", 100, "REF-1", "Ref Match One"),
		makeTxNamed("l2", 100, "REF-2", "Ref Match Two"),
		makeTxNamed("l3", 100, "REF-3", "Ref Match Three"),
		makeTxNamed("l4", 100, "REF-4", "Ref Match Four"),
		makeTxNamed("l5", 100, "", "Name Only Alpha"),
		makeTxNamed("l6", 100, "", "Name Only Beta"),
		makeTxNamed("l7", 100, "", "Name Only Gamma"),
	}
	right := []Transaction{
		makeTxNamed("r1", 100, "REF-1", "Ref Match One"),
		makeTxNamed("r2", 100, "REF-2", "Ref Match Two"),
		makeTxNamed("r3", 100, "REF-3", "Ref Match Three"),
		makeTxNamed("r4", 100, "REF-4", "Ref Match Four"),
		makeTxNamed("r5", 100, "", "Name Only Alpha"),
		makeTxNamed("r6", 100, "", "Name Only Beta"),
		makeTxNamed("r7", 100, "", "Name Only Gamma"),
	}
	batchRes, err := Reconcile("p", "left", "right", left, right, pair)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if batchRes.Summary.MatchedCount != wantMatched {
		t.Errorf("batch MatchedCount=%d want %d", batchRes.Summary.MatchedCount, wantMatched)
	}
	assertRowCoverage(t, left, right, batchRes)
	assertMonetaryInvariant(t, batchRes.Summary)
	assertSummaryMatchesCounts(t, batchRes)

	runStreaming := func(t *testing.T, label string, idx RightIndex) Summary {
		t.Helper()
		var out bytes.Buffer
		w := newJSONWriter(&out)
		w.SetMeta("p", "left", "right")
		if err := ReconcileStreaming(
			context.Background(), "p", "left", "right",
			leftPath, rightPath, parserCfg, parserCfg, pair, idx, w, 0,
		); err != nil {
			t.Fatalf("%s ReconcileStreaming: %v", label, err)
		}
		var res Result
		if err := json.Unmarshal(out.Bytes(), &res); err != nil {
			t.Fatalf("%s unmarshal: %v", label, err)
		}
		return res.Summary
	}

	// --- Streaming + memory index ---
	memIdx := NewMemoryIndex()
	defer memIdx.Close()
	memSummary := runStreaming(t, "streaming-memory", memIdx)
	if memSummary.MatchedCount != wantMatched {
		t.Errorf("streaming-memory MatchedCount=%d want %d", memSummary.MatchedCount, wantMatched)
	}
	if memSummary.UnmatchedLeft != wantUnmatchedLeft {
		t.Errorf("streaming-memory UnmatchedLeft=%d want %d", memSummary.UnmatchedLeft, wantUnmatchedLeft)
	}
	if memSummary.UnmatchedRight != wantUnmatchedRight {
		t.Errorf("streaming-memory UnmatchedRight=%d want %d", memSummary.UnmatchedRight, wantUnmatchedRight)
	}

	// --- Streaming + disk index ---
	diskIdx, err := NewDiskIndex(t.TempDir())
	if err != nil {
		t.Fatalf("NewDiskIndex: %v", err)
	}
	defer diskIdx.Close()
	diskSummary := runStreaming(t, "streaming-disk", diskIdx)
	if diskSummary.MatchedCount != wantMatched {
		t.Errorf("streaming-disk MatchedCount=%d want %d", diskSummary.MatchedCount, wantMatched)
	}
	if diskSummary.UnmatchedLeft != wantUnmatchedLeft {
		t.Errorf("streaming-disk UnmatchedLeft=%d want %d", diskSummary.UnmatchedLeft, wantUnmatchedLeft)
	}
	if diskSummary.UnmatchedRight != wantUnmatchedRight {
		t.Errorf("streaming-disk UnmatchedRight=%d want %d", diskSummary.UnmatchedRight, wantUnmatchedRight)
	}

	// All three paths must agree on every Summary field.
	compareSummaries := func(t *testing.T, label string, got, want Summary) {
		t.Helper()
		if got.MatchedCount != want.MatchedCount {
			t.Errorf("%s vs batch: MatchedCount %d != %d", label, got.MatchedCount, want.MatchedCount)
		}
		if got.UnmatchedLeft != want.UnmatchedLeft {
			t.Errorf("%s vs batch: UnmatchedLeft %d != %d", label, got.UnmatchedLeft, want.UnmatchedLeft)
		}
		if got.UnmatchedRight != want.UnmatchedRight {
			t.Errorf("%s vs batch: UnmatchedRight %d != %d", label, got.UnmatchedRight, want.UnmatchedRight)
		}
		if got.TotalDiscrepancy != want.TotalDiscrepancy {
			t.Errorf("%s vs batch: TotalDiscrepancy %d != %d", label, got.TotalDiscrepancy, want.TotalDiscrepancy)
		}
	}
	compareSummaries(t, "streaming-memory", memSummary, batchRes.Summary)
	compareSummaries(t, "streaming-disk", diskSummary, batchRes.Summary)
}

// ---------------------------------------------------------------------------
// one_to_many pass tests
// ---------------------------------------------------------------------------

func oneToManyPair() config.Pair {
	return config.Pair{
		DateWindow:           "0d",
		AmountToleranceMinor: 0,
		Passes:               []config.PassConfig{{Type: config.PassTypeOneToMany}},
	}
}

// Test G — basic grouped match: 1 left, 3 rights, sum exact.
func TestOneToMany_BasicGroupedMatch(t *testing.T) {
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	left := []Transaction{makeTxFull("l1", 300, "INV-001", "", base)}
	right := []Transaction{
		makeTxFull("r1", 100, "INV-001", "", base),
		makeTxFull("r2", 100, "INV-001", "", base),
		makeTxFull("r3", 100, "INV-001", "", base),
	}
	res, err := Reconcile("p", "left", "right", left, right, oneToManyPair())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.GroupedMatched) != 1 {
		t.Fatalf("GroupedMatched=%d want 1", len(res.GroupedMatched))
	}
	if len(res.GroupedMatched[0].Rights) != 3 {
		t.Errorf("GroupedMatched[0].Rights=%d want 3", len(res.GroupedMatched[0].Rights))
	}
	if res.Summary.GroupedMatchedCount != 1 {
		t.Errorf("GroupedMatchedCount=%d want 1", res.Summary.GroupedMatchedCount)
	}
	if res.Summary.AmbiguousGroupCount != 0 {
		t.Errorf("AmbiguousGroupCount=%d want 0", res.Summary.AmbiguousGroupCount)
	}
	assertRowCoverage(t, left, right, res)
	assertMonetaryInvariant(t, res.Summary)
	assertSummaryMatchesCounts(t, res)
}

// Test H — grouped amount diff: sum outside tolerance.
func TestOneToMany_GroupedAmountDiff(t *testing.T) {
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	left := []Transaction{makeTxFull("l1", 300, "INV-002", "", base)}
	right := []Transaction{
		makeTxFull("r1", 100, "INV-002", "", base),
		makeTxFull("r2", 100, "INV-002", "", base),
		// sum=250, diff=50 > tolerance=0
		makeTxFull("r3", 50, "INV-002", "", base),
	}
	res, err := Reconcile("p", "left", "right", left, right, oneToManyPair())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.GroupedAmountDiff) != 1 {
		t.Fatalf("GroupedAmountDiff=%d want 1", len(res.GroupedAmountDiff))
	}
	if res.GroupedAmountDiff[0].DiffMinor != 50 {
		t.Errorf("DiffMinor=%d want 50", res.GroupedAmountDiff[0].DiffMinor)
	}
	assertRowCoverage(t, left, right, res)
	assertMonetaryInvariant(t, res.Summary)
	assertSummaryMatchesCounts(t, res)
}

// Test I — grouped timing diff: amounts reconcile within tolerance but a right date
// is outside the window.
func TestOneToMany_GroupedTimingDiff(t *testing.T) {
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	late := time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC) // 9 days
	pair := config.Pair{
		DateWindow:           "5d",
		AmountToleranceMinor: 0,
		Passes:               []config.PassConfig{{Type: config.PassTypeOneToMany}},
	}
	left := []Transaction{makeTxFull("l1", 200, "INV-003", "", base)}
	right := []Transaction{
		makeTxFull("r1", 100, "INV-003", "", base),
		makeTxFull("r2", 100, "INV-003", "", late), // 9 days > 5d window
	}
	res, err := Reconcile("p", "left", "right", left, right, pair)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.GroupedTimingDiff) != 1 {
		t.Fatalf("GroupedTimingDiff=%d want 1", len(res.GroupedTimingDiff))
	}
	if res.GroupedTimingDiff[0].DaysDiff != 9 {
		t.Errorf("DaysDiff=%d want 9", res.GroupedTimingDiff[0].DaysDiff)
	}
	assertRowCoverage(t, left, right, res)
	assertMonetaryInvariant(t, res.Summary)
	assertSummaryMatchesCounts(t, res)
}

// Test J — both amount and date fail: left stays unmatched, rights NOT consumed
// (available for a later pass or carry-forward).
func TestOneToMany_BothFail_RightsNotConsumed(t *testing.T) {
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	late := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	pair := config.Pair{
		DateWindow:           "5d",
		AmountToleranceMinor: 0,
		Passes:               []config.PassConfig{{Type: config.PassTypeOneToMany}},
	}
	left := []Transaction{makeTxFull("l1", 300, "INV-004", "", base)}
	right := []Transaction{
		makeTxFull("r1", 80, "INV-004", "", late), // amount fails and date fails
		makeTxFull("r2", 80, "INV-004", "", late), // amount fails and date fails
	}
	res, err := Reconcile("p", "left", "right", left, right, pair)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.UnmatchedLeft) != 1 {
		t.Errorf("UnmatchedLeft=%d want 1", len(res.UnmatchedLeft))
	}
	if len(res.UnmatchedRight) != 2 {
		t.Errorf("UnmatchedRight=%d want 2 (rights must not be consumed)", len(res.UnmatchedRight))
	}
	if len(res.GroupedMatched)+len(res.GroupedAmountDiff)+len(res.GroupedTimingDiff) != 0 {
		t.Errorf("expected no grouped outcomes when both amount and date fail")
	}
	assertRowCoverage(t, left, right, res)
	assertMonetaryInvariant(t, res.Summary)
	assertSummaryMatchesCounts(t, res)
}

// Test K — group size one: a single right row for a reference is treated as a
// group of 1, not as a 1-to-1 match (different classification bucket).
func TestOneToMany_GroupSizeOne(t *testing.T) {
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	left := []Transaction{makeTxFull("l1", 100, "INV-005", "", base)}
	right := []Transaction{makeTxFull("r1", 100, "INV-005", "", base)}
	res, err := Reconcile("p", "left", "right", left, right, oneToManyPair())
	if err != nil {
		t.Fatal(err)
	}
	// one_to_many groups all rights by reference, so even 1 right is GroupedMatched.
	if len(res.GroupedMatched) != 1 {
		t.Fatalf("GroupedMatched=%d want 1 (single-right group)", len(res.GroupedMatched))
	}
	if len(res.Matched) != 0 {
		t.Errorf("Matched=%d want 0 (one_to_many must not emit to Matched)", len(res.Matched))
	}
	assertRowCoverage(t, left, right, res)
	assertMonetaryInvariant(t, res.Summary)
	assertSummaryMatchesCounts(t, res)
}

// Test L — ambiguous group: 2 left rows share a reference → AmbiguousGroupPair
// emitted, rows excluded from matching, monetary invariant holds.
func TestOneToMany_AmbiguousGroup(t *testing.T) {
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	left := []Transaction{
		makeTxFull("l1", 300, "INV-006", "", base),
		makeTxFull("l2", 300, "INV-006", "", base), // same reference — ambiguous
	}
	right := []Transaction{
		makeTxFull("r1", 100, "INV-006", "", base),
		makeTxFull("r2", 100, "INV-006", "", base),
		makeTxFull("r3", 100, "INV-006", "", base),
	}
	res, err := Reconcile("p", "left", "right", left, right, oneToManyPair())
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary.AmbiguousGroupCount != 1 {
		t.Errorf("AmbiguousGroupCount=%d want 1", res.Summary.AmbiguousGroupCount)
	}
	if len(res.AmbiguousGroups[0].LeftRows) != 2 {
		t.Errorf("AmbiguousGroups[0].LeftRows=%d want 2", len(res.AmbiguousGroups[0].LeftRows))
	}
	if len(res.AmbiguousGroups[0].Rights) != 3 {
		t.Errorf("AmbiguousGroups[0].Rights=%d want 3", len(res.AmbiguousGroups[0].Rights))
	}
	// Ambiguous rows must not appear in any matched/unmatched slice.
	if len(res.UnmatchedLeft) != 0 || len(res.GroupedMatched) != 0 {
		t.Errorf("ambiguous rows must not appear in matched or unmatched slices")
	}
	// TotalDiscrepancy must include the ambiguous amounts.
	if res.Summary.AmbiguousAmountLeft != 600 {
		t.Errorf("AmbiguousAmountLeft=%d want 600", res.Summary.AmbiguousAmountLeft)
	}
	if res.Summary.AmbiguousAmountRight != 300 {
		t.Errorf("AmbiguousAmountRight=%d want 300", res.Summary.AmbiguousAmountRight)
	}
	assertRowCoverage(t, left, right, res)
	assertMonetaryInvariant(t, res.Summary)
	assertSummaryMatchesCounts(t, res)
}

// Test M — pipeline [reference_one_to_one, one_to_many]: the reference pass
// handles clean 1-to-1 rows first; installments that fail BOTH amount and date
// in the reference pass survive to the one_to_many pass.
//
// With a 5-day window and tolerance=0:
//   - Each installment right ($100) differs from l2 ($200) in amount → !amtOk
//   - Each installment right is 11 days late → !dateOk
//   - Ref pass: both fail → l2 unmatched, r2/r3 not consumed
//   - one_to_many: sum(r2+r3)=200=l2.Amount → amtOk; maxDaysDiff=11>5 → GroupedTimingDiff
func TestOneToMany_AfterRefPass(t *testing.T) {
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	late := time.Date(2024, 1, 12, 0, 0, 0, 0, time.UTC) // 11 days off — outside 5d window
	pair := config.Pair{
		DateWindow:           "5d",
		AmountToleranceMinor: 0,
		Passes: []config.PassConfig{
			{Type: config.PassTypeReferenceOneToOne},
			{Type: config.PassTypeOneToMany},
		},
	}
	left := []Transaction{
		makeTxFull("l1", 100, "SIMPLE-001", "", base),
		makeTxFull("l2", 200, "INV-007", "", base),
	}
	right := []Transaction{
		makeTxFull("r1", 100, "SIMPLE-001", "", base),
		makeTxFull("r2", 100, "INV-007", "", late),
		makeTxFull("r3", 100, "INV-007", "", late),
	}
	res, err := Reconcile("p", "left", "right", left, right, pair)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Matched) != 1 || res.Matched[0].Left.ID != "l1" {
		t.Errorf("Matched=%d, expected exactly l1 from reference pass", len(res.Matched))
	}
	// Installments survive the ref pass (both fail there) and land in GroupedTimingDiff
	// in the one_to_many pass (amounts sum correctly, but dates are still outside window).
	if len(res.GroupedTimingDiff) != 1 || res.GroupedTimingDiff[0].Left.ID != "l2" {
		t.Errorf("GroupedTimingDiff=%d want 1 with l2 (from one_to_many pass)", len(res.GroupedTimingDiff))
	}
	if len(res.UnmatchedLeft)+len(res.UnmatchedRight) != 0 {
		t.Errorf("expected no unmatched rows, got left=%d right=%d",
			len(res.UnmatchedLeft), len(res.UnmatchedRight))
	}
	assertRowCoverage(t, left, right, res)
	assertMonetaryInvariant(t, res.Summary)
	assertSummaryMatchesCounts(t, res)
}
