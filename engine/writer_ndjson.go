package engine

import (
	"encoding/json"
	"io"
)

// ---------------------------------------------------------------------------
// NDJSONWriter — one tagged JSON envelope per event, immediately written.
// O(1) memory. Crash-safe: each line is independently valid JSON.
// When SetRunInfo is called, it emits a {"type":"run_info",...} line immediately,
// before any match/unmatched events, making it the first line of output.
// ---------------------------------------------------------------------------

type ndjsonWriter struct {
	enc *json.Encoder
}

func newNDJSONWriter(w io.Writer) *ndjsonWriter {
	return &ndjsonWriter{enc: json.NewEncoder(w)}
}

type ndjsonEnvelope struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

func (n *ndjsonWriter) emit(typ string, data any) error {
	return n.enc.Encode(ndjsonEnvelope{Type: typ, Data: data})
}

// SetRunInfo emits the run_info line immediately as the first line of output.
// Implements RunInfoSetter. Must be called before ReconcileStreaming begins parsing.
func (n *ndjsonWriter) SetRunInfo(info RunInfo) error {
	return n.emit("run_info", info)
}

// SetIndexSelection emits the selection after run_info when audit mode is
// enabled, and as the first metadata event otherwise.
func (n *ndjsonWriter) SetIndexSelection(selection IndexSelection) error {
	return n.emit("index_selection", selection)
}

func (n *ndjsonWriter) WriteMatch(pair MatchedPair) error         { return n.emit("match", pair) }
func (n *ndjsonWriter) WriteAmountDiff(pair AmountDiffPair) error { return n.emit("amount_diff", pair) }
func (n *ndjsonWriter) WriteTimingDiff(pair TimingDiffPair) error { return n.emit("timing_diff", pair) }
func (n *ndjsonWriter) WriteUnmatched(tx Transaction, side string) error {
	return n.emit("unmatched_"+side, tx)
}
func (n *ndjsonWriter) WriteDuplicate(group DuplicateGroup) error { return n.emit("duplicate", group) }
func (n *ndjsonWriter) WriteSummary(s Summary) error              { return n.emit("summary", s) }
func (n *ndjsonWriter) Flush() error                              { return nil }

// sourceSummary is the payload for a "source_summary" ndjson line.
type sourceSummary struct {
	Source  string  `json:"source"`
	Summary Summary `json:"summary"`
}

// WriteSourceSummary emits one source_summary line per counterpart for 1-N
// source runs. Implements SourceBreakdownWriter.
func (n *ndjsonWriter) WriteSourceSummary(sourceName string, s Summary) error {
	return n.emit("source_summary", sourceSummary{Source: sourceName, Summary: s})
}

// GroupedEventWriter implementation — one tagged line per event.
func (n *ndjsonWriter) WriteGroupedMatch(pair GroupedMatchedPair) error {
	return n.emit(eventGroupedMatch, pair)
}
func (n *ndjsonWriter) WriteGroupedAmountDiff(pair GroupedAmountDiffPair) error {
	return n.emit(keyGroupedAmountDiff, pair)
}
func (n *ndjsonWriter) WriteGroupedTimingDiff(pair GroupedTimingDiffPair) error {
	return n.emit(keyGroupedTimingDiff, pair)
}
func (n *ndjsonWriter) WriteAmbiguousGroup(pair AmbiguousGroupPair) error {
	return n.emit(eventAmbiguousGroup, pair)
}

// ManyToManyEventWriter implementation — one tagged line per event.
func (n *ndjsonWriter) WriteManyToManyMatch(pair ManyToManyMatchedPair) error {
	return n.emit(eventManyToManyMatch, pair)
}
func (n *ndjsonWriter) WriteManyToManyAmountDiff(pair ManyToManyAmountDiffPair) error {
	return n.emit(keyManyToManyAmountDiff, pair)
}
func (n *ndjsonWriter) WriteManyToManyTimingDiff(pair ManyToManyTimingDiffPair) error {
	return n.emit(keyManyToManyTimingDiff, pair)
}
