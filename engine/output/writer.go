// Package output renders reconciliation results.
//
//nolint:staticcheck // Domain aliases keep package-internal signatures readable.
package output

import (
	"fmt"
	"io"

	. "github.com/reconifyhq/reconify/engine/domain"
)

// ResultWriter receives reconciliation events and writes them to an output format.
//
// Not thread-safe. Must be called from a single goroutine.
// Flush() must be called exactly once after all events have been written.
type ResultWriter interface {
	WriteMatch(pair MatchedPair) error
	WriteAmountDiff(pair AmountDiffPair) error
	WriteTimingDiff(pair TimingDiffPair) error
	// WriteUnmatched writes an unmatched transaction. side must be "left" or "right".
	WriteUnmatched(tx Transaction, side string) error
	WriteDuplicate(group DuplicateGroup) error
	WriteSummary(s Summary) error
	// Flush finalizes the output (closes JSON arrays/objects, flushes CSV buffers,
	// renders table). Must be called exactly once after all events.
	Flush() error
}

// RunInfoSetter is an optional interface implemented by writers that support
// embedding run provenance metadata in their output. ReconcileStreaming calls
// this via type assertion before any events are written, so for streaming writers
// (ndjson) the run_info line is guaranteed to be the first line of output.
//
// Writers that do not implement this interface silently skip run_info (csv, table).
type RunInfoSetter interface {
	SetRunInfo(info RunInfo) error
}

// IndexSelectionSetter is implemented by structured writers that can expose
// the resource-aware backend decision in their output.
type IndexSelectionSetter interface {
	SetIndexSelection(selection IndexSelection) error
}

// SourceBreakdownWriter is an optional interface implemented by writers that can
// surface a per-counterpart summary for 1-N source runs (see
// ReconcileMultiSource / ReconcileStreamingMultiSource). It is called once per
// counterpart, in pass order, before the final aggregate WriteSummary call.
//
// Writers that do not implement this interface silently skip the breakdown —
// the aggregate Summary passed to WriteSummary is unaffected either way.
type SourceBreakdownWriter interface {
	WriteSourceSummary(sourceName string, s Summary) error
}

// GroupedEventWriter is an optional interface implemented by writers that can render
// grouped and ambiguous match events produced by the one_to_many pass. Writers that
// do not implement this interface (csv, table) skip these events — the CLI warns
// when this occurs so data loss is never silent.
type GroupedEventWriter interface {
	WriteGroupedMatch(pair GroupedMatchedPair) error
	WriteGroupedAmountDiff(pair GroupedAmountDiffPair) error
	WriteGroupedTimingDiff(pair GroupedTimingDiffPair) error
	WriteAmbiguousGroup(pair AmbiguousGroupPair) error
}

// ManyToManyEventWriter is an optional interface implemented by writers that can
// render grouped match events produced by the many_to_many pass.
type ManyToManyEventWriter interface {
	WriteManyToManyMatch(pair ManyToManyMatchedPair) error
	WriteManyToManyAmountDiff(pair ManyToManyAmountDiffPair) error
	WriteManyToManyTimingDiff(pair ManyToManyTimingDiffPair) error
}

// SubsetSumEventWriter is an optional interface implemented by writers that can
// render events produced by the subset_sum pass. Writers that do not implement
// this interface (csv, table) skip these events — the CLI warns when this occurs.
type SubsetSumEventWriter interface {
	WriteSubsetSumMatch(pair SubsetSumMatchedPair) error
	WriteSubsetSumAmbiguous(pair SubsetSumAmbiguousPair) error
	WriteSubsetSumSkipped(pair SubsetSumSkippedPair) error
}

// FinancialEventWriter renders source-local fee and settlement checks.
type FinancialEventWriter interface {
	WriteFinancialEffectMatch(FinancialEffectFinding) error
	WriteFinancialEffectDiff(FinancialEffectFinding) error
	WriteFinancialUnchecked(FinancialEffectFinding) error
	WriteSettlementMatch(SettlementFinding) error
	WriteSettlementDiff(SettlementFinding) error
}

// JSON section key names for grouped/ambiguous slices (used by jsonStreamWriter)
// and ndjson event type tags (used by ndjsonWriter).
// The JSON section keys mirror the Result struct field names (plural);
// the ndjson tags follow the event-name convention (singular action noun).
// grouped_amount_diff and grouped_timing_diff are identical in both forms.
const (
	keyGroupedMatched       = "grouped_matched"
	keyGroupedAmountDiff    = "grouped_amount_diff"
	keyGroupedTimingDiff    = "grouped_timing_diff"
	keyAmbiguousGroups      = "ambiguous_groups"
	keyManyToManyMatched    = "many_to_many_matched"
	keyManyToManyAmountDiff = "many_to_many_amount_diff"
	keyManyToManyTimingDiff = "many_to_many_timing_diff"

	eventGroupedMatch    = "grouped_match"      // ndjson type tag (singular)
	eventAmbiguousGroup  = "ambiguous_group"    // ndjson type tag (singular)
	eventManyToManyMatch = "many_to_many_match" // ndjson type tag (singular)

	keySubsetSumMatched   = "subset_sum_matched"
	keySubsetSumAmbiguous = "subset_sum_ambiguous"
	keySubsetSumSkipped   = "subset_sum_skipped"

	eventSubsetSumMatch     = "subset_sum_match"     // ndjson type tag (singular)
	eventSubsetSumAmbiguous = "subset_sum_ambiguous" // ndjson type tag (singular)
	eventSubsetSumSkipped   = "subset_sum_skipped"   // ndjson type tag (singular)
)

// NewResultWriter returns a ResultWriter for the given format name.
// Valid formats: "json", "json-stream", "ndjson", "csv", "table".
func NewResultWriter(format string, w io.Writer) (ResultWriter, error) {
	switch format {
	case "json":
		return newJSONWriter(w), nil
	case "json-stream":
		return newJSONStreamWriter(w), nil
	case "ndjson":
		return newNDJSONWriter(w), nil
	case "csv":
		return newCSVWriter(w), nil
	case "table":
		return newTableWriter(w), nil
	default:
		return nil, fmt.Errorf("unknown format %q (valid: json, json-stream, ndjson, csv, table)", format)
	}
}
