package output_test

import (
	"testing"
	"time"

	"github.com/reconifyhq/reconify/config"
	. "github.com/reconifyhq/reconify/engine/domain"
	"github.com/reconifyhq/reconify/engine/matching"
	. "github.com/reconifyhq/reconify/engine/output"
	"github.com/reconifyhq/reconify/engine/reconcile"
)

// captureWriter records every call made to it and is used as the inner writer
// for filteredWriter tests.
type captureWriter struct {
	matches       []MatchedPair
	amountDiffs   []AmountDiffPair
	timingDiffs   []TimingDiffPair
	unmatched     []Transaction
	duplicates    []DuplicateGroup
	summaries     []Summary
	flushed       int
	runInfos      []RunInfo
	idxSelections []IndexSelection
	sourceSums    map[string]Summary
	groupedMatch  []GroupedMatchedPair
	groupedAmt    []GroupedAmountDiffPair
	groupedTiming []GroupedTimingDiffPair
	ambiguous     []AmbiguousGroupPair
	m2mMatch      []ManyToManyMatchedPair
	m2mAmt        []ManyToManyAmountDiffPair
	m2mTiming     []ManyToManyTimingDiffPair
}

func (c *captureWriter) WriteMatch(p MatchedPair) error {
	c.matches = append(c.matches, p)
	return nil
}
func (c *captureWriter) WriteAmountDiff(p AmountDiffPair) error {
	c.amountDiffs = append(c.amountDiffs, p)
	return nil
}
func (c *captureWriter) WriteTimingDiff(p TimingDiffPair) error {
	c.timingDiffs = append(c.timingDiffs, p)
	return nil
}
func (c *captureWriter) WriteUnmatched(tx Transaction, side string) error {
	c.unmatched = append(c.unmatched, tx)
	return nil
}
func (c *captureWriter) WriteDuplicate(g DuplicateGroup) error {
	c.duplicates = append(c.duplicates, g)
	return nil
}
func (c *captureWriter) WriteSummary(s Summary) error {
	c.summaries = append(c.summaries, s)
	return nil
}
func (c *captureWriter) Flush() error {
	c.flushed++
	return nil
}
func (c *captureWriter) SetRunInfo(info RunInfo) error {
	c.runInfos = append(c.runInfos, info)
	return nil
}
func (c *captureWriter) SetIndexSelection(sel IndexSelection) error {
	c.idxSelections = append(c.idxSelections, sel)
	return nil
}
func (c *captureWriter) WriteSourceSummary(name string, s Summary) error {
	if c.sourceSums == nil {
		c.sourceSums = make(map[string]Summary)
	}
	c.sourceSums[name] = s
	return nil
}
func (c *captureWriter) WriteGroupedMatch(p GroupedMatchedPair) error {
	c.groupedMatch = append(c.groupedMatch, p)
	return nil
}
func (c *captureWriter) WriteGroupedAmountDiff(p GroupedAmountDiffPair) error {
	c.groupedAmt = append(c.groupedAmt, p)
	return nil
}
func (c *captureWriter) WriteGroupedTimingDiff(p GroupedTimingDiffPair) error {
	c.groupedTiming = append(c.groupedTiming, p)
	return nil
}
func (c *captureWriter) WriteAmbiguousGroup(p AmbiguousGroupPair) error {
	c.ambiguous = append(c.ambiguous, p)
	return nil
}
func (c *captureWriter) WriteManyToManyMatch(p ManyToManyMatchedPair) error {
	c.m2mMatch = append(c.m2mMatch, p)
	return nil
}
func (c *captureWriter) WriteManyToManyAmountDiff(p ManyToManyAmountDiffPair) error {
	c.m2mAmt = append(c.m2mAmt, p)
	return nil
}
func (c *captureWriter) WriteManyToManyTimingDiff(p ManyToManyTimingDiffPair) error {
	c.m2mTiming = append(c.m2mTiming, p)
	return nil
}

var _ ResultWriter = (*captureWriter)(nil)
var _ RunInfoSetter = (*captureWriter)(nil)
var _ IndexSelectionSetter = (*captureWriter)(nil)
var _ SourceBreakdownWriter = (*captureWriter)(nil)
var _ GroupedEventWriter = (*captureWriter)(nil)
var _ ManyToManyEventWriter = (*captureWriter)(nil)

func testTxWithAmount(id string, amount int64, ref string) Transaction {
	return Transaction{ID: id, Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), Amount: amount, Currency: "USD", Reference: ref}
}

func testTx(id string) Transaction {
	return Transaction{ID: id, Date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), Amount: 100, Currency: "USD", Reference: id}
}

func TestFilteredWriter_ModeAll_PassesEverything(t *testing.T) {
	inner := &captureWriter{}
	w := WrapWithResultMode(inner, config.ResultModeAll, "")

	_ = w.WriteMatch(MatchedPair{Left: testTx("l1"), Right: testTx("r1")})
	_ = w.WriteAmountDiff(AmountDiffPair{Left: testTx("l2"), Right: testTx("r2"), DiffMinor: 10})
	_ = w.WriteTimingDiff(TimingDiffPair{Left: testTx("l3"), Right: testTx("r3"), DaysDiff: 2})
	_ = w.WriteUnmatched(testTx("l4"), "left")
	_ = w.WriteDuplicate(DuplicateGroup{Source: "left", Reference: "r", Transactions: []Transaction{testTx("l5")}})
	_ = w.WriteSummary(Summary{MatchedCount: 1})
	_ = w.Flush()

	if len(inner.matches) != 1 {
		t.Errorf("matches: got %d, want 1", len(inner.matches))
	}
	if len(inner.amountDiffs) != 1 {
		t.Errorf("amountDiffs: got %d, want 1", len(inner.amountDiffs))
	}
	if len(inner.timingDiffs) != 1 {
		t.Errorf("timingDiffs: got %d, want 1", len(inner.timingDiffs))
	}
	if len(inner.unmatched) != 1 {
		t.Errorf("unmatched: got %d, want 1", len(inner.unmatched))
	}
	if len(inner.duplicates) != 1 {
		t.Errorf("duplicates: got %d, want 1", len(inner.duplicates))
	}
	if inner.flushed != 1 {
		t.Errorf("flushed: got %d, want 1", inner.flushed)
	}
}

func TestFilteredWriter_ModeExceptionsOnly_SuppressesCleanMatches(t *testing.T) {
	inner := &captureWriter{}
	w := WrapWithResultMode(inner, config.ResultModeExceptionsOnly, "")

	_ = w.WriteMatch(MatchedPair{Left: testTx("l1"), Right: testTx("r1")})
	_ = w.WriteAmountDiff(AmountDiffPair{Left: testTx("l2"), Right: testTx("r2"), DiffMinor: 10})
	_ = w.WriteTimingDiff(TimingDiffPair{Left: testTx("l3"), Right: testTx("r3"), DaysDiff: 2})
	_ = w.WriteUnmatched(testTx("l4"), "left")
	_ = w.WriteDuplicate(DuplicateGroup{Source: "left", Reference: "r", Transactions: []Transaction{testTx("l5")}})
	_ = w.WriteSummary(Summary{MatchedCount: 1})

	if len(inner.matches) != 0 {
		t.Errorf("exceptions_only: matches should be suppressed, got %d", len(inner.matches))
	}
	if len(inner.amountDiffs) != 1 {
		t.Errorf("exceptions_only: amountDiffs should pass through, got %d", len(inner.amountDiffs))
	}
	if len(inner.timingDiffs) != 1 {
		t.Errorf("exceptions_only: timingDiffs should pass through, got %d", len(inner.timingDiffs))
	}
	if len(inner.unmatched) != 1 {
		t.Errorf("exceptions_only: unmatched should pass through, got %d", len(inner.unmatched))
	}
	if len(inner.duplicates) != 1 {
		t.Errorf("exceptions_only: duplicates should pass through, got %d", len(inner.duplicates))
	}
}

func TestFilteredWriter_ModeSummaryOnly_SuppressesAllItems(t *testing.T) {
	inner := &captureWriter{}
	w := WrapWithResultMode(inner, config.ResultModeSummaryOnly, "")

	_ = w.WriteMatch(MatchedPair{Left: testTx("l1"), Right: testTx("r1")})
	_ = w.WriteAmountDiff(AmountDiffPair{Left: testTx("l2"), Right: testTx("r2"), DiffMinor: 10})
	_ = w.WriteTimingDiff(TimingDiffPair{Left: testTx("l3"), Right: testTx("r3"), DaysDiff: 2})
	_ = w.WriteUnmatched(testTx("l4"), "left")
	_ = w.WriteDuplicate(DuplicateGroup{Source: "left", Reference: "r", Transactions: []Transaction{testTx("l5")}})
	_ = w.WriteSummary(Summary{MatchedCount: 1, TotalLeft: 5})
	_ = w.Flush()

	if len(inner.matches) != 0 {
		t.Errorf("summary_only: matches should be suppressed")
	}
	if len(inner.amountDiffs) != 0 {
		t.Errorf("summary_only: amountDiffs should be suppressed")
	}
	if len(inner.timingDiffs) != 0 {
		t.Errorf("summary_only: timingDiffs should be suppressed")
	}
	if len(inner.unmatched) != 0 {
		t.Errorf("summary_only: unmatched should be suppressed")
	}
	if len(inner.duplicates) != 0 {
		t.Errorf("summary_only: duplicates should be suppressed")
	}
	if len(inner.summaries) != 1 {
		t.Errorf("summary_only: summary should still be written, got %d", len(inner.summaries))
	}
	if inner.flushed != 1 {
		t.Errorf("summary_only: flush should still be called, got %d", inner.flushed)
	}
}

func TestFilteredWriter_PatchesSummaryResultMode(t *testing.T) {
	inner := &captureWriter{}
	w := WrapWithResultMode(inner, config.ResultModeExceptionsOnly, "")
	_ = w.WriteSummary(Summary{MatchedCount: 5})

	if len(inner.summaries) != 1 {
		t.Fatal("expected one summary")
	}
	if got := inner.summaries[0].ResultMode; got != string(config.ResultModeExceptionsOnly) {
		t.Errorf("Summary.ResultMode = %q, want %q", got, config.ResultModeExceptionsOnly)
	}
}

func TestFilteredWriter_PatchesSummaryRunID(t *testing.T) {
	inner := &captureWriter{}
	w := WrapWithResultMode(inner, config.ResultModeAll, "run-abc-123")
	_ = w.WriteSummary(Summary{MatchedCount: 5})

	if len(inner.summaries) != 1 {
		t.Fatal("expected one summary")
	}
	if got := inner.summaries[0].RunID; got != "run-abc-123" {
		t.Errorf("Summary.RunID = %q, want %q", got, "run-abc-123")
	}
}

func TestFilteredWriter_EmptyMode_DefaultsToAll(t *testing.T) {
	inner := &captureWriter{}
	w := WrapWithResultMode(inner, "", "") // empty → all

	_ = w.WriteMatch(MatchedPair{Left: testTx("l1"), Right: testTx("r1")})
	if len(inner.matches) != 1 {
		t.Errorf("empty mode: expected match to pass through, got %d", len(inner.matches))
	}
}

func TestFilteredWriter_ForwardsOptionalInterfaces(t *testing.T) {
	inner := &captureWriter{}
	w := WrapWithResultMode(inner, config.ResultModeAll, "")

	// RunInfoSetter
	if setter, ok := w.(RunInfoSetter); ok {
		_ = setter.SetRunInfo(RunInfo{RunID: "test-run"})
		if len(inner.runInfos) != 1 || inner.runInfos[0].RunID != "test-run" {
			t.Error("SetRunInfo not forwarded")
		}
	} else {
		t.Error("filteredWriter should implement RunInfoSetter")
	}

	// IndexSelectionSetter
	if setter, ok := w.(IndexSelectionSetter); ok {
		_ = setter.SetIndexSelection(IndexSelection{Backend: "memory"})
		if len(inner.idxSelections) != 1 {
			t.Error("SetIndexSelection not forwarded")
		}
	} else {
		t.Error("filteredWriter should implement IndexSelectionSetter")
	}

	// SourceBreakdownWriter
	if sbw, ok := w.(SourceBreakdownWriter); ok {
		_ = sbw.WriteSourceSummary("src1", Summary{MatchedCount: 3})
		if inner.sourceSums["src1"].MatchedCount != 3 {
			t.Error("WriteSourceSummary not forwarded")
		}
	} else {
		t.Error("filteredWriter should implement SourceBreakdownWriter")
	}
}

func TestFilteredWriter_GroupedEvents_ExceptionsOnly_SuppressesMatches(t *testing.T) {
	inner := &captureWriter{}
	w := WrapWithResultMode(inner, config.ResultModeExceptionsOnly, "")

	gw, ok := w.(GroupedEventWriter)
	if !ok {
		t.Fatal("filteredWriter should implement GroupedEventWriter")
	}

	_ = gw.WriteGroupedMatch(GroupedMatchedPair{Left: testTx("l1"), Rights: []Transaction{testTx("r1")}})
	_ = gw.WriteGroupedAmountDiff(GroupedAmountDiffPair{Left: testTx("l2"), Rights: []Transaction{testTx("r2")}, DiffMinor: 5})
	_ = gw.WriteGroupedTimingDiff(GroupedTimingDiffPair{Left: testTx("l3"), Rights: []Transaction{testTx("r3")}, DaysDiff: 1})
	_ = gw.WriteAmbiguousGroup(AmbiguousGroupPair{Reference: "r", LeftRows: []Transaction{testTx("l4")}, Rights: []Transaction{testTx("r4")}})

	if len(inner.groupedMatch) != 0 {
		t.Errorf("exceptions_only: grouped match should be suppressed, got %d", len(inner.groupedMatch))
	}
	if len(inner.groupedAmt) != 1 {
		t.Errorf("exceptions_only: grouped amount diff should pass through, got %d", len(inner.groupedAmt))
	}
	if len(inner.groupedTiming) != 1 {
		t.Errorf("exceptions_only: grouped timing diff should pass through, got %d", len(inner.groupedTiming))
	}
	if len(inner.ambiguous) != 1 {
		t.Errorf("exceptions_only: ambiguous groups should pass through, got %d", len(inner.ambiguous))
	}
}

func TestFilteredWriter_ManyToManyEvents_ExceptionsOnly_SuppressesMatches(t *testing.T) {
	inner := &captureWriter{}
	w := WrapWithResultMode(inner, config.ResultModeExceptionsOnly, "")

	mw, ok := w.(ManyToManyEventWriter)
	if !ok {
		t.Fatal("filteredWriter should implement ManyToManyEventWriter")
	}

	_ = mw.WriteManyToManyMatch(ManyToManyMatchedPair{Lefts: []Transaction{testTx("l1")}, Rights: []Transaction{testTx("r1")}})
	_ = mw.WriteManyToManyAmountDiff(ManyToManyAmountDiffPair{Lefts: []Transaction{testTx("l2")}, Rights: []Transaction{testTx("r2")}, DiffMinor: 5})
	_ = mw.WriteManyToManyTimingDiff(ManyToManyTimingDiffPair{Lefts: []Transaction{testTx("l3")}, Rights: []Transaction{testTx("r3")}, DaysDiff: 2})

	if len(inner.m2mMatch) != 0 {
		t.Errorf("exceptions_only: m2m match should be suppressed, got %d", len(inner.m2mMatch))
	}
	if len(inner.m2mAmt) != 1 {
		t.Errorf("exceptions_only: m2m amount diff should pass through, got %d", len(inner.m2mAmt))
	}
	if len(inner.m2mTiming) != 1 {
		t.Errorf("exceptions_only: m2m timing diff should pass through, got %d", len(inner.m2mTiming))
	}
}

func TestFilteredWriter_SummaryOnly_SuppressesGroupedAndM2M(t *testing.T) {
	inner := &captureWriter{}
	w := WrapWithResultMode(inner, config.ResultModeSummaryOnly, "")

	gw, _ := w.(GroupedEventWriter)
	mw, _ := w.(ManyToManyEventWriter)

	if gw != nil {
		_ = gw.WriteGroupedMatch(GroupedMatchedPair{Left: testTx("l1"), Rights: []Transaction{testTx("r1")}})
		_ = gw.WriteGroupedAmountDiff(GroupedAmountDiffPair{Left: testTx("l2"), Rights: []Transaction{testTx("r2")}})
		_ = gw.WriteGroupedTimingDiff(GroupedTimingDiffPair{Left: testTx("l3"), Rights: []Transaction{testTx("r3")}})
		_ = gw.WriteAmbiguousGroup(AmbiguousGroupPair{Reference: "r"})
	}
	if mw != nil {
		_ = mw.WriteManyToManyMatch(ManyToManyMatchedPair{Lefts: []Transaction{testTx("l4")}, Rights: []Transaction{testTx("r4")}})
		_ = mw.WriteManyToManyAmountDiff(ManyToManyAmountDiffPair{})
		_ = mw.WriteManyToManyTimingDiff(ManyToManyTimingDiffPair{})
	}

	if len(inner.groupedMatch)+len(inner.groupedAmt)+len(inner.groupedTiming)+len(inner.ambiguous) != 0 {
		t.Error("summary_only: all grouped events should be suppressed")
	}
	if len(inner.m2mMatch)+len(inner.m2mAmt)+len(inner.m2mTiming) != 0 {
		t.Error("summary_only: all m2m events should be suppressed")
	}
}

func TestFilteredWriter_ReconcileIntegration_ExceptionsOnly(t *testing.T) {
	left := []Transaction{
		testTxWithAmount("l1", 100, "MATCH"),
		testTxWithAmount("l2", 200, "DIFF"),
		testTxWithAmount("l3", 300, "ONLY-L"),
	}
	right := []Transaction{
		testTxWithAmount("r1", 100, "MATCH"),
		testTxWithAmount("r2", 250, "DIFF"),
		testTxWithAmount("r3", 400, "ONLY-R"),
	}
	inner := &captureWriter{}
	w := WrapWithResultMode(inner, config.ResultModeExceptionsOnly, "run-42")
	result, err := reconcile.Reconcile("pair", "left", "right", left, right, config.Pair{DateWindow: "0d"})
	if err != nil {
		t.Fatal(err)
	}
	// Drain result through the writer (same as drainResultToWriter does).
	for _, mp := range result.Matched {
		_ = w.WriteMatch(mp)
	}
	for _, ap := range result.AmountDiff {
		_ = w.WriteAmountDiff(ap)
	}
	for _, tx := range result.UnmatchedLeft {
		_ = w.WriteUnmatched(tx, "left")
	}
	for _, tx := range result.UnmatchedRight {
		_ = w.WriteUnmatched(tx, "right")
	}
	_ = w.WriteSummary(result.Summary)
	_ = w.Flush()

	// Clean match should be suppressed.
	if len(inner.matches) != 0 {
		t.Errorf("exceptions_only integration: clean matches should be suppressed, got %d", len(inner.matches))
	}
	// Amount diff (DIFF) and unmatched (ONLY-L, ONLY-R) should pass through.
	if len(inner.amountDiffs) != 1 {
		t.Errorf("exceptions_only integration: expected 1 amount diff, got %d", len(inner.amountDiffs))
	}
	if len(inner.unmatched) != 2 {
		t.Errorf("exceptions_only integration: expected 2 unmatched, got %d", len(inner.unmatched))
	}
	// Summary should still be written and patched.
	if len(inner.summaries) != 1 {
		t.Fatal("expected one summary")
	}
	if got := inner.summaries[0].ResultMode; got != string(config.ResultModeExceptionsOnly) {
		t.Errorf("Summary.ResultMode = %q, want %q", got, config.ResultModeExceptionsOnly)
	}
	if got := inner.summaries[0].RunID; got != "run-42" {
		t.Errorf("Summary.RunID = %q, want %q", got, "run-42")
	}
	// Classification counts are unchanged — filtering is at writer boundary.
	if inner.summaries[0].MatchedCount != 1 {
		t.Errorf("MatchedCount in summary should reflect actual reconciliation, got %d", inner.summaries[0].MatchedCount)
	}
}

func TestFilteredWriter_ReconcileIntegration_SummaryOnly(t *testing.T) {
	left := []Transaction{testTxWithAmount("l1", 100, "MATCH"), testTxWithAmount("l2", 200, "ONLY-L")}
	right := []Transaction{testTxWithAmount("r1", 100, "MATCH")}
	inner := &captureWriter{}
	w := WrapWithResultMode(inner, config.ResultModeSummaryOnly, "")
	result, err := reconcile.Reconcile("pair", "left", "right", left, right, config.Pair{DateWindow: "0d"})
	if err != nil {
		t.Fatal(err)
	}
	for _, mp := range result.Matched {
		_ = w.WriteMatch(mp)
	}
	for _, tx := range result.UnmatchedLeft {
		_ = w.WriteUnmatched(tx, "left")
	}
	_ = w.WriteSummary(result.Summary)
	_ = w.Flush()

	if len(inner.matches) != 0 || len(inner.unmatched) != 0 {
		t.Error("summary_only: all item events should be suppressed")
	}
	if len(inner.summaries) != 1 {
		t.Errorf("summary_only: expected 1 summary, got %d", len(inner.summaries))
	}
	if inner.summaries[0].ResultMode != string(config.ResultModeSummaryOnly) {
		t.Errorf("Summary.ResultMode = %q, want %q", inner.summaries[0].ResultMode, config.ResultModeSummaryOnly)
	}
}

func TestSummaryCurrencyField(t *testing.T) {
	left := []Transaction{testTxWithAmount("l1", 100, "MATCH")}
	right := []Transaction{testTxWithAmount("r1", 100, "MATCH")}
	result, err := reconcile.Reconcile("pair", "left", "right", left, right, config.Pair{DateWindow: "0d"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary.Currency != "USD" {
		t.Errorf("Summary.Currency = %q, want %q", result.Summary.Currency, "USD")
	}
}

func TestSummaryRunIDFromOptions(t *testing.T) {
	left := []Transaction{testTxWithAmount("l1", 100, "REF")}
	right := []Transaction{testTxWithAmount("r1", 100, "REF")}
	result, err := reconcile.Reconcile("pair", "left", "right", left, right, config.Pair{DateWindow: "0d"},
		matching.ReconcileOptions{RunID: "test-run-id"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary.RunID != "test-run-id" {
		t.Errorf("Summary.RunID = %q, want %q", result.Summary.RunID, "test-run-id")
	}
}

func TestSummaryResultModeFromOptions(t *testing.T) {
	left := []Transaction{testTxWithAmount("l1", 100, "REF")}
	right := []Transaction{testTxWithAmount("r1", 100, "REF")}
	result, err := reconcile.Reconcile("pair", "left", "right", left, right, config.Pair{DateWindow: "0d"},
		matching.ReconcileOptions{ResultMode: config.ResultModeExceptionsOnly})
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary.ResultMode != string(config.ResultModeExceptionsOnly) {
		t.Errorf("Summary.ResultMode = %q, want %q", result.Summary.ResultMode, config.ResultModeExceptionsOnly)
	}
}
