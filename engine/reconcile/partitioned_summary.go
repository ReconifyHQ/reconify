//nolint:staticcheck // Domain aliases keep package-internal signatures readable.
package reconcile

import (
	"math"

	. "github.com/reconifyhq/reconify/engine/domain"
)

type partitionSummaryWriter struct {
	ResultWriter
	summary       Summary
	seen          bool
	warnedGrouped bool
	warnedMany    bool
}

func (w *partitionSummaryWriter) WriteSummary(s Summary) error {
	w.summary = addSummaries(w.summary, s)
	w.seen = true
	return nil
}

func (w *partitionSummaryWriter) Flush() error { return nil }

func (w *partitionSummaryWriter) ObserveWarning(warning Warning) {
	observeWarning(w.ResultWriter, nil, warning)
}

func (w *partitionSummaryWriter) warnGroupedEventsUnsupported() {
	if w.warnedGrouped {
		return
	}
	w.warnedGrouped = true
	w.ObserveWarning(Warning{Code: WarningUnsupportedGroupedEvents, Message: "current output format does not support grouped or ambiguous match events; use --format=json or --format=ndjson to capture all one_to_many output"})
}

func (w *partitionSummaryWriter) warnManyToManyEventsUnsupported() {
	if w.warnedMany {
		return
	}
	w.warnedMany = true
	w.ObserveWarning(Warning{Code: WarningUnsupportedManyToManyEvents, Message: "current output format does not support many_to_many match events; use --format=json or --format=ndjson to capture all many_to_many output"})
}

// GroupedEventWriter forwarding — prevents the embedded ResultWriter interface
// from hiding the inner concrete writer's optional methods.
func (w *partitionSummaryWriter) WriteGroupedMatch(p GroupedMatchedPair) error {
	if gw, ok := w.ResultWriter.(GroupedEventWriter); ok {
		return gw.WriteGroupedMatch(p)
	}
	w.warnGroupedEventsUnsupported()
	return nil
}
func (w *partitionSummaryWriter) WriteGroupedAmountDiff(p GroupedAmountDiffPair) error {
	if gw, ok := w.ResultWriter.(GroupedEventWriter); ok {
		return gw.WriteGroupedAmountDiff(p)
	}
	w.warnGroupedEventsUnsupported()
	return nil
}
func (w *partitionSummaryWriter) WriteGroupedTimingDiff(p GroupedTimingDiffPair) error {
	if gw, ok := w.ResultWriter.(GroupedEventWriter); ok {
		return gw.WriteGroupedTimingDiff(p)
	}
	w.warnGroupedEventsUnsupported()
	return nil
}
func (w *partitionSummaryWriter) WriteAmbiguousGroup(p AmbiguousGroupPair) error {
	if gw, ok := w.ResultWriter.(GroupedEventWriter); ok {
		return gw.WriteAmbiguousGroup(p)
	}
	w.warnGroupedEventsUnsupported()
	return nil
}

// ManyToManyEventWriter forwarding.
func (w *partitionSummaryWriter) WriteManyToManyMatch(p ManyToManyMatchedPair) error {
	if mw, ok := w.ResultWriter.(ManyToManyEventWriter); ok {
		return mw.WriteManyToManyMatch(p)
	}
	w.warnManyToManyEventsUnsupported()
	return nil
}
func (w *partitionSummaryWriter) WriteManyToManyAmountDiff(p ManyToManyAmountDiffPair) error {
	if mw, ok := w.ResultWriter.(ManyToManyEventWriter); ok {
		return mw.WriteManyToManyAmountDiff(p)
	}
	w.warnManyToManyEventsUnsupported()
	return nil
}
func (w *partitionSummaryWriter) WriteManyToManyTimingDiff(p ManyToManyTimingDiffPair) error {
	if mw, ok := w.ResultWriter.(ManyToManyEventWriter); ok {
		return mw.WriteManyToManyTimingDiff(p)
	}
	w.warnManyToManyEventsUnsupported()
	return nil
}

// SubsetSumEventWriter forwarding.
func (w *partitionSummaryWriter) WriteSubsetSumMatch(p SubsetSumMatchedPair) error {
	if sw, ok := w.ResultWriter.(SubsetSumEventWriter); ok {
		return sw.WriteSubsetSumMatch(p)
	}
	return nil
}
func (w *partitionSummaryWriter) WriteSubsetSumAmbiguous(p SubsetSumAmbiguousPair) error {
	if sw, ok := w.ResultWriter.(SubsetSumEventWriter); ok {
		return sw.WriteSubsetSumAmbiguous(p)
	}
	return nil
}
func (w *partitionSummaryWriter) WriteSubsetSumSkipped(p SubsetSumSkippedPair) error {
	if sw, ok := w.ResultWriter.(SubsetSumEventWriter); ok {
		return sw.WriteSubsetSumSkipped(p)
	}
	return nil
}

// SourceBreakdownWriter forwarding.
func (w *partitionSummaryWriter) WriteSourceSummary(sourceName string, sum Summary) error {
	if sbw, ok := w.ResultWriter.(SourceBreakdownWriter); ok {
		return sbw.WriteSourceSummary(sourceName, sum)
	}
	return nil
}

func (w *partitionSummaryWriter) FlushSummary() error {
	if !w.seen {
		return w.ResultWriter.WriteSummary(Summary{})
	}
	if err := w.ResultWriter.WriteSummary(w.summary); err != nil {
		return err
	}
	return w.ResultWriter.Flush()
}

func addSummaries(a, b Summary) Summary {
	if a.Currency == "" && b.Currency != "" {
		a.Currency = b.Currency
	}
	if a.ResultMode == "" && b.ResultMode != "" {
		a.ResultMode = b.ResultMode
	}
	if a.RunID == "" && b.RunID != "" {
		a.RunID = b.RunID
	}
	a.TotalLeft += b.TotalLeft
	a.TotalRight += b.TotalRight
	a.MatchedCount += b.MatchedCount
	a.UnmatchedLeft += b.UnmatchedLeft
	a.UnmatchedRight += b.UnmatchedRight
	a.AmountDiffCount += b.AmountDiffCount
	a.TimingDiffCount += b.TimingDiffCount
	a.DuplicateCount += b.DuplicateCount
	a.GroupedMatchedCount += b.GroupedMatchedCount
	a.GroupedAmountDiffCount += b.GroupedAmountDiffCount
	a.GroupedTimingDiffCount += b.GroupedTimingDiffCount
	a.ManyToManyMatchedCount += b.ManyToManyMatchedCount
	a.ManyToManyAmountDiffCount += b.ManyToManyAmountDiffCount
	a.ManyToManyTimingDiffCount += b.ManyToManyTimingDiffCount
	a.AmbiguousGroupCount += b.AmbiguousGroupCount
	a.SubsetSumMatchedCount += b.SubsetSumMatchedCount
	a.SubsetSumAmbiguousCount += b.SubsetSumAmbiguousCount
	a.SubsetSumSkippedCount += b.SubsetSumSkippedCount
	a.MatchedAmountLeft += b.MatchedAmountLeft
	a.MatchedAmountRight += b.MatchedAmountRight
	a.UnmatchedAmountLeft += b.UnmatchedAmountLeft
	a.UnmatchedAmountRight += b.UnmatchedAmountRight
	a.AmountDiffTotal += b.AmountDiffTotal
	a.AmbiguousAmountLeft += b.AmbiguousAmountLeft
	a.AmbiguousAmountRight += b.AmbiguousAmountRight
	a.TotalDiscrepancy += b.TotalDiscrepancy
	denom := a.TotalLeft
	if a.TotalRight > denom {
		denom = a.TotalRight
	}
	if denom > 0 {
		a.MatchRatePct = math.Round(float64(a.MatchedCount)/float64(denom)*10000) / 100
		reconciledCount := a.MatchedCount + a.AmountDiffCount + a.TimingDiffCount +
			a.GroupedMatchedCount + a.GroupedAmountDiffCount + a.GroupedTimingDiffCount +
			a.ManyToManyMatchedCount + a.ManyToManyAmountDiffCount + a.ManyToManyTimingDiffCount +
			a.SubsetSumMatchedCount
		a.ReconciledRatePct = math.Round(float64(reconciledCount)/float64(denom)*10000) / 100
	}
	return a
}
