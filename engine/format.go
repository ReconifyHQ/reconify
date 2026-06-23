package engine

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"text/tabwriter"
	"time"
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

// JSON section key names for grouped/ambiguous slices (used by jsonStreamWriter)
// and ndjson event type tags (used by ndjsonWriter).
// The JSON section keys mirror the Result struct field names (plural);
// the ndjson tags follow the event-name convention (singular action noun).
// grouped_amount_diff and grouped_timing_diff are identical in both forms.
const (
	keyGroupedMatched    = "grouped_matched"
	keyGroupedAmountDiff = "grouped_amount_diff"
	keyGroupedTimingDiff = "grouped_timing_diff"
	keyAmbiguousGroups   = "ambiguous_groups"

	eventGroupedMatch   = "grouped_match"    // ndjson type tag (singular)
	eventAmbiguousGroup = "ambiguous_group"  // ndjson type tag (singular)
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

// ---------------------------------------------------------------------------
// JSONWriter — buffers full Result struct, writes indented JSON on Flush().
// Supports SetRunInfo (embeds RunInfo in Result.RunInfo) and SetDeterministic
// (sorts all output sections before encoding for stable diff-based audit trails).
// ---------------------------------------------------------------------------

type jsonWriter struct {
	w             io.Writer
	result        Result
	deterministic bool
}

func newJSONWriter(w io.Writer) *jsonWriter { return &jsonWriter{w: w} }

func (j *jsonWriter) WriteMatch(pair MatchedPair) error {
	j.result.Matched = append(j.result.Matched, pair)
	return nil
}
func (j *jsonWriter) WriteAmountDiff(pair AmountDiffPair) error {
	j.result.AmountDiff = append(j.result.AmountDiff, pair)
	return nil
}
func (j *jsonWriter) WriteTimingDiff(pair TimingDiffPair) error {
	j.result.TimingDiff = append(j.result.TimingDiff, pair)
	return nil
}
func (j *jsonWriter) WriteUnmatched(tx Transaction, side string) error {
	if side == "left" {
		j.result.UnmatchedLeft = append(j.result.UnmatchedLeft, tx)
	} else {
		j.result.UnmatchedRight = append(j.result.UnmatchedRight, tx)
	}
	return nil
}
func (j *jsonWriter) WriteDuplicate(group DuplicateGroup) error {
	j.result.Duplicates = append(j.result.Duplicates, group)
	return nil
}
func (j *jsonWriter) WriteSummary(s Summary) error {
	j.result.Summary = s
	return nil
}

// WriteSourceSummary records a per-counterpart breakdown for 1-N source runs.
// Implements SourceBreakdownWriter.
func (j *jsonWriter) WriteSourceSummary(sourceName string, s Summary) error {
	if j.result.BySource == nil {
		j.result.BySource = make(map[string]Summary)
	}
	j.result.BySource[sourceName] = s
	return nil
}

// GroupedEventWriter implementation — appends to the result struct fields flushed by Flush().
func (j *jsonWriter) WriteGroupedMatch(pair GroupedMatchedPair) error {
	j.result.GroupedMatched = append(j.result.GroupedMatched, pair)
	return nil
}
func (j *jsonWriter) WriteGroupedAmountDiff(pair GroupedAmountDiffPair) error {
	j.result.GroupedAmountDiff = append(j.result.GroupedAmountDiff, pair)
	return nil
}
func (j *jsonWriter) WriteGroupedTimingDiff(pair GroupedTimingDiffPair) error {
	j.result.GroupedTimingDiff = append(j.result.GroupedTimingDiff, pair)
	return nil
}
func (j *jsonWriter) WriteAmbiguousGroup(pair AmbiguousGroupPair) error {
	j.result.AmbiguousGroups = append(j.result.AmbiguousGroups, pair)
	return nil
}

// SetMeta sets pair and source names on the result. Fixes the pre-existing bug
// where PairName/LeftSource/RightSource were never populated in the JSON output.
func (j *jsonWriter) SetMeta(pairName, leftSource, rightSource string) {
	j.result.PairName = pairName
	j.result.LeftSource = leftSource
	j.result.RightSource = rightSource
}

// SetRunInfo stores the audit envelope for inclusion in the JSON output.
// Implements RunInfoSetter.
func (j *jsonWriter) SetRunInfo(info RunInfo) error {
	j.result.RunInfo = &info
	return nil
}

// SetDeterministic enables stable output ordering. When true, Flush() sorts all
// result sections by a stable key before encoding. This adds O(n log n) sort time
// on the result set — typically 4-8 seconds at 17M matched rows.
func (j *jsonWriter) SetDeterministic(on bool) { j.deterministic = on }

// sortResult sorts all result sections in place for deterministic output.
func (j *jsonWriter) sortResult() {
	sort.Slice(j.result.Matched, func(i, k int) bool {
		return j.result.Matched[i].Left.ID < j.result.Matched[k].Left.ID
	})
	sort.Slice(j.result.UnmatchedLeft, func(i, k int) bool {
		return j.result.UnmatchedLeft[i].ID < j.result.UnmatchedLeft[k].ID
	})
	sort.Slice(j.result.UnmatchedRight, func(i, k int) bool {
		return j.result.UnmatchedRight[i].ID < j.result.UnmatchedRight[k].ID
	})
	sort.Slice(j.result.AmountDiff, func(i, k int) bool {
		return j.result.AmountDiff[i].Left.ID < j.result.AmountDiff[k].Left.ID
	})
	sort.Slice(j.result.TimingDiff, func(i, k int) bool {
		return j.result.TimingDiff[i].Left.ID < j.result.TimingDiff[k].Left.ID
	})
	sort.Slice(j.result.Duplicates, func(i, k int) bool {
		if j.result.Duplicates[i].Reference != j.result.Duplicates[k].Reference {
			return j.result.Duplicates[i].Reference < j.result.Duplicates[k].Reference
		}
		return j.result.Duplicates[i].Source < j.result.Duplicates[k].Source
	})
	sort.Slice(j.result.GroupedMatched, func(i, k int) bool {
		return j.result.GroupedMatched[i].Left.ID < j.result.GroupedMatched[k].Left.ID
	})
	sort.Slice(j.result.GroupedAmountDiff, func(i, k int) bool {
		return j.result.GroupedAmountDiff[i].Left.ID < j.result.GroupedAmountDiff[k].Left.ID
	})
	sort.Slice(j.result.GroupedTimingDiff, func(i, k int) bool {
		return j.result.GroupedTimingDiff[i].Left.ID < j.result.GroupedTimingDiff[k].Left.ID
	})
	sort.Slice(j.result.AmbiguousGroups, func(i, k int) bool {
		return j.result.AmbiguousGroups[i].Reference < j.result.AmbiguousGroups[k].Reference
	})
}

func (j *jsonWriter) Flush() error {
	if j.deterministic {
		j.sortResult()
	}
	enc := json.NewEncoder(j.w)
	enc.SetIndent("", "  ")
	return enc.Encode(j.result)
}

// GetResult returns the accumulated Result. Used by the batch Reconcile() wrapper.
func (j *jsonWriter) GetResult() *Result { return &j.result }

// ---------------------------------------------------------------------------
// JSONStreamWriter — buffers JSON bytes per section; immediate encoding on
// each event. More GC-friendly than JSONWriter since Go structs are released
// after encoding, but JSON bytes still accumulate. This is not O(1) memory; use
// NDJSONWriter or CSVWriter when memory must stay constant with result size.
// Structurally identical output to JSONWriter.
//
// Note: if the process is interrupted mid-stream, output will be invalid JSON
// (unclosed arrays/object). NDJSON does not have this problem — each line is
// independently valid. For crash-safe output, prefer --format=ndjson.
// ---------------------------------------------------------------------------

type jsonStreamSection struct {
	key    string
	events []json.RawMessage
}

type jsonStreamWriter struct {
	w        io.Writer
	meta     struct{ PairName, LeftSource, RightSource string }
	runInfo  *RunInfo
	sections []jsonStreamSection // ordered; keyed by JSON field name
	byKey    map[string]*jsonStreamSection
	summary  *Summary
	bySource map[string]Summary
	// grouped/ambiguous events from one_to_many pass — only emitted when non-empty
	groupedMatched    []json.RawMessage
	groupedAmountDiff []json.RawMessage
	groupedTimingDiff []json.RawMessage
	ambiguousGroups   []json.RawMessage
}

func newJSONStreamWriter(w io.Writer) *jsonStreamWriter {
	order := []string{"matched", "unmatched_left", "unmatched_right", "amount_diff", "timing_diff", "duplicates"}
	jw := &jsonStreamWriter{
		w:     w,
		byKey: make(map[string]*jsonStreamSection, len(order)),
	}
	for _, k := range order {
		sec := &jsonStreamSection{key: k}
		jw.sections = append(jw.sections, *sec)
	}
	// rebuild map from slice to point to slice elements
	for i := range jw.sections {
		jw.byKey[jw.sections[i].key] = &jw.sections[i]
	}
	return jw
}

func (j *jsonStreamWriter) encode(v any) (json.RawMessage, error) {
	b, err := json.Marshal(v)
	return json.RawMessage(b), err
}

func (j *jsonStreamWriter) appendTo(key string, v any) error {
	b, err := j.encode(v)
	if err != nil {
		return err
	}
	j.byKey[key].events = append(j.byKey[key].events, b)
	return nil
}

func (j *jsonStreamWriter) WriteMatch(pair MatchedPair) error {
	return j.appendTo("matched", pair)
}
func (j *jsonStreamWriter) WriteAmountDiff(pair AmountDiffPair) error {
	return j.appendTo("amount_diff", pair)
}
func (j *jsonStreamWriter) WriteTimingDiff(pair TimingDiffPair) error {
	return j.appendTo("timing_diff", pair)
}
func (j *jsonStreamWriter) WriteUnmatched(tx Transaction, side string) error {
	if side == "left" {
		return j.appendTo("unmatched_left", tx)
	}
	return j.appendTo("unmatched_right", tx)
}
func (j *jsonStreamWriter) WriteDuplicate(group DuplicateGroup) error {
	return j.appendTo("duplicates", group)
}
func (j *jsonStreamWriter) WriteSummary(s Summary) error {
	j.summary = &s
	return nil
}

// WriteSourceSummary records a per-counterpart breakdown for 1-N source runs.
// Implements SourceBreakdownWriter.
func (j *jsonStreamWriter) WriteSourceSummary(sourceName string, s Summary) error {
	if j.bySource == nil {
		j.bySource = make(map[string]Summary)
	}
	j.bySource[sourceName] = s
	return nil
}

// GroupedEventWriter implementation — buffers encoded JSON; emitted in Flush() only when non-empty.
func (j *jsonStreamWriter) WriteGroupedMatch(pair GroupedMatchedPair) error {
	b, err := j.encode(pair)
	if err != nil {
		return err
	}
	j.groupedMatched = append(j.groupedMatched, b)
	return nil
}
func (j *jsonStreamWriter) WriteGroupedAmountDiff(pair GroupedAmountDiffPair) error {
	b, err := j.encode(pair)
	if err != nil {
		return err
	}
	j.groupedAmountDiff = append(j.groupedAmountDiff, b)
	return nil
}
func (j *jsonStreamWriter) WriteGroupedTimingDiff(pair GroupedTimingDiffPair) error {
	b, err := j.encode(pair)
	if err != nil {
		return err
	}
	j.groupedTimingDiff = append(j.groupedTimingDiff, b)
	return nil
}
func (j *jsonStreamWriter) WriteAmbiguousGroup(pair AmbiguousGroupPair) error {
	b, err := j.encode(pair)
	if err != nil {
		return err
	}
	j.ambiguousGroups = append(j.ambiguousGroups, b)
	return nil
}

func (j *jsonStreamWriter) Flush() error {
	result := map[string]any{
		"pair":         j.meta.PairName,
		"left_source":  j.meta.LeftSource,
		"right_source": j.meta.RightSource,
	}
	if j.runInfo != nil {
		result["run_info"] = j.runInfo
	}
	if j.summary != nil {
		result["summary"] = j.summary
	} else {
		result["summary"] = Summary{}
	}
	if len(j.bySource) > 0 {
		result["by_source"] = j.bySource
	}
	for _, sec := range j.sections {
		if len(sec.events) == 0 {
			result[sec.key] = []json.RawMessage{}
		} else {
			result[sec.key] = sec.events
		}
	}
	// Grouped/ambiguous sections from one_to_many pass — only emitted when non-empty
	// to preserve backwards-compatible output for runs without one_to_many.
	if len(j.groupedMatched) > 0 {
		result[keyGroupedMatched] = j.groupedMatched
	}
	if len(j.groupedAmountDiff) > 0 {
		result[keyGroupedAmountDiff] = j.groupedAmountDiff
	}
	if len(j.groupedTimingDiff) > 0 {
		result[keyGroupedTimingDiff] = j.groupedTimingDiff
	}
	if len(j.ambiguousGroups) > 0 {
		result[keyAmbiguousGroups] = j.ambiguousGroups
	}
	enc := json.NewEncoder(j.w)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}

// SetMeta sets the pair/source metadata for the JSON output.
func (j *jsonStreamWriter) SetMeta(pairName, leftSource, rightSource string) {
	j.meta.PairName = pairName
	j.meta.LeftSource = leftSource
	j.meta.RightSource = rightSource
}

// SetRunInfo stores the audit envelope for inclusion in the JSON-stream output.
// Implements RunInfoSetter.
func (j *jsonStreamWriter) SetRunInfo(info RunInfo) error {
	j.runInfo = &info
	return nil
}

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

// ---------------------------------------------------------------------------
// CSVWriter — fixed schema, one row per event. O(1) memory. Versioned contract.
//
// Column order:
//
//	type, left_id, left_date, left_amount_minor, left_ref, left_name,
//	right_id, right_date, right_amount_minor, right_ref, right_name,
//	diff_minor, days_diff,
//	source, reference, dup_count,
//	total_left, total_right, matched, unmatched_left, unmatched_right,
//	amount_diff_count, timing_diff_count, duplicate_count, match_rate_pct,
//	matched_amount_left, matched_amount_right, unmatched_amount_left,
//	unmatched_amount_right, amount_diff_total, total_discrepancy, reconciled_rate_pct
//
// Unused columns for a given event type are empty string.
// CSVWriter does not implement RunInfoSetter — audit users should use json formats.
// ---------------------------------------------------------------------------

var csvHeader = []string{
	"type",
	"left_id", "left_date", "left_amount_minor", "left_ref", "left_name",
	"right_id", "right_date", "right_amount_minor", "right_ref", "right_name",
	"diff_minor", "days_diff",
	"source", "reference", "dup_count",
	"total_left", "total_right", "matched", "unmatched_left", "unmatched_right",
	"amount_diff_count", "timing_diff_count", "duplicate_count", "match_rate_pct",
	"matched_amount_left", "matched_amount_right", "unmatched_amount_left",
	"unmatched_amount_right", "amount_diff_total", "total_discrepancy", "reconciled_rate_pct",
}

type csvWriter struct {
	w       *csv.Writer
	started bool
}

func newCSVWriter(w io.Writer) *csvWriter {
	return &csvWriter{w: csv.NewWriter(w)}
}

func (c *csvWriter) writeHeader() error {
	if c.started {
		return nil
	}
	c.started = true
	return c.w.Write(csvHeader)
}

// emptyRow returns a slice of empty strings with len == len(csvHeader).
func emptyRow(typ string) []string {
	row := make([]string, len(csvHeader))
	row[0] = typ
	return row
}

func fmtDate(t time.Time) string { return t.Format(time.RFC3339) }
func fmtI64(v int64) string      { return strconv.FormatInt(v, 10) }
func fmtInt(v int) string        { return strconv.Itoa(v) }

// SanitizeCSVField prevents spreadsheet formula injection by prefixing cells
// that start with a formula trigger character with a single quote.
func SanitizeCSVField(s string) string {
	if len(s) == 0 {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + s
	}
	return s
}

func txLeft(row []string, tx Transaction) {
	row[1] = tx.ID
	row[2] = fmtDate(tx.Date)
	row[3] = fmtI64(tx.Amount)
	row[4] = SanitizeCSVField(tx.Reference)
	row[5] = SanitizeCSVField(tx.Name)
}

func txRight(row []string, tx Transaction) {
	row[6] = tx.ID
	row[7] = fmtDate(tx.Date)
	row[8] = fmtI64(tx.Amount)
	row[9] = SanitizeCSVField(tx.Reference)
	row[10] = SanitizeCSVField(tx.Name)
}

func (c *csvWriter) WriteMatch(pair MatchedPair) error {
	if err := c.writeHeader(); err != nil {
		return err
	}
	row := emptyRow("match")
	txLeft(row, pair.Left)
	txRight(row, pair.Right)
	return c.w.Write(row)
}

func (c *csvWriter) WriteAmountDiff(pair AmountDiffPair) error {
	if err := c.writeHeader(); err != nil {
		return err
	}
	row := emptyRow("amount_diff")
	txLeft(row, pair.Left)
	txRight(row, pair.Right)
	row[11] = fmtI64(pair.DiffMinor)
	return c.w.Write(row)
}

func (c *csvWriter) WriteTimingDiff(pair TimingDiffPair) error {
	if err := c.writeHeader(); err != nil {
		return err
	}
	row := emptyRow("timing_diff")
	txLeft(row, pair.Left)
	txRight(row, pair.Right)
	row[12] = fmtInt(pair.DaysDiff)
	return c.w.Write(row)
}

func (c *csvWriter) WriteUnmatched(tx Transaction, side string) error {
	if err := c.writeHeader(); err != nil {
		return err
	}
	typ := "unmatched_" + side
	row := emptyRow(typ)
	if side == "left" {
		txLeft(row, tx)
	} else {
		txRight(row, tx)
	}
	return c.w.Write(row)
}

func (c *csvWriter) WriteDuplicate(group DuplicateGroup) error {
	if err := c.writeHeader(); err != nil {
		return err
	}
	row := emptyRow("duplicate")
	row[13] = group.Source
	row[14] = SanitizeCSVField(group.Reference)
	row[15] = fmtInt(len(group.Transactions))
	return c.w.Write(row)
}

func (c *csvWriter) WriteSummary(s Summary) error {
	if err := c.writeHeader(); err != nil {
		return err
	}
	row := emptyRow("summary")
	row[16] = fmtInt(s.TotalLeft)
	row[17] = fmtInt(s.TotalRight)
	row[18] = fmtInt(s.MatchedCount)
	row[19] = fmtInt(s.UnmatchedLeft)
	row[20] = fmtInt(s.UnmatchedRight)
	row[21] = fmtInt(s.AmountDiffCount)
	row[22] = fmtInt(s.TimingDiffCount)
	row[23] = fmtInt(s.DuplicateCount)
	row[24] = strconv.FormatFloat(s.MatchRatePct, 'f', 2, 64)
	row[25] = fmtI64(s.MatchedAmountLeft)
	row[26] = fmtI64(s.MatchedAmountRight)
	row[27] = fmtI64(s.UnmatchedAmountLeft)
	row[28] = fmtI64(s.UnmatchedAmountRight)
	row[29] = fmtI64(s.AmountDiffTotal)
	row[30] = fmtI64(s.TotalDiscrepancy)
	row[31] = strconv.FormatFloat(s.ReconciledRatePct, 'f', 2, 64)
	return c.w.Write(row)
}

func (c *csvWriter) Flush() error {
	c.w.Flush()
	return c.w.Error()
}

// ---------------------------------------------------------------------------
// TableWriter — buffers all rows, renders ASCII table via text/tabwriter on Flush().
// Not suitable for large datasets: warns at tableWarnThreshold rows.
// TableWriter does not implement RunInfoSetter — it is a human display tool only.
// ---------------------------------------------------------------------------

const tableWarnThreshold = 10_000

type tableRow struct {
	typ      string
	leftID   string
	leftDt   string
	leftAmt  string
	leftRef  string
	rightID  string
	rightDt  string
	rightAmt string
	rightRef string
	note     string
}

type tableWriter struct {
	w    io.Writer
	rows []tableRow
	warn bool
}

func newTableWriter(w io.Writer) *tableWriter { return &tableWriter{w: w} }

func (t *tableWriter) maybeWarn() {
	if !t.warn && len(t.rows) > tableWarnThreshold {
		t.warn = true
		if _, err := fmt.Fprintf(t.w, "warning: table mode has buffered >%d rows; for large files use --format=ndjson or --format=csv\n", tableWarnThreshold); err != nil {
			return
		}
	}
}

func (t *tableWriter) WriteMatch(pair MatchedPair) error {
	t.rows = append(t.rows, tableRow{
		typ:      "match",
		leftID:   pair.Left.ID,
		leftDt:   pair.Left.Date.Format("2006-01-02"),
		leftAmt:  fmtI64(pair.Left.Amount),
		leftRef:  pair.Left.Reference,
		rightID:  pair.Right.ID,
		rightDt:  pair.Right.Date.Format("2006-01-02"),
		rightAmt: fmtI64(pair.Right.Amount),
		rightRef: pair.Right.Reference,
	})
	t.maybeWarn()
	return nil
}

func (t *tableWriter) WriteAmountDiff(pair AmountDiffPair) error {
	t.rows = append(t.rows, tableRow{
		typ:      "amount_diff",
		leftID:   pair.Left.ID,
		leftDt:   pair.Left.Date.Format("2006-01-02"),
		leftAmt:  fmtI64(pair.Left.Amount),
		leftRef:  pair.Left.Reference,
		rightID:  pair.Right.ID,
		rightDt:  pair.Right.Date.Format("2006-01-02"),
		rightAmt: fmtI64(pair.Right.Amount),
		rightRef: pair.Right.Reference,
		note:     "diff=" + fmtI64(pair.DiffMinor),
	})
	t.maybeWarn()
	return nil
}

func (t *tableWriter) WriteTimingDiff(pair TimingDiffPair) error {
	t.rows = append(t.rows, tableRow{
		typ:      "timing_diff",
		leftID:   pair.Left.ID,
		leftDt:   pair.Left.Date.Format("2006-01-02"),
		leftAmt:  fmtI64(pair.Left.Amount),
		leftRef:  pair.Left.Reference,
		rightID:  pair.Right.ID,
		rightDt:  pair.Right.Date.Format("2006-01-02"),
		rightAmt: fmtI64(pair.Right.Amount),
		rightRef: pair.Right.Reference,
		note:     "days=" + fmtInt(pair.DaysDiff),
	})
	t.maybeWarn()
	return nil
}

func (t *tableWriter) WriteUnmatched(tx Transaction, side string) error {
	row := tableRow{
		typ:  "unmatched_" + side,
		note: "",
	}
	if side == "left" {
		row.leftID = tx.ID
		row.leftDt = tx.Date.Format("2006-01-02")
		row.leftAmt = fmtI64(tx.Amount)
		row.leftRef = tx.Reference
	} else {
		row.rightID = tx.ID
		row.rightDt = tx.Date.Format("2006-01-02")
		row.rightAmt = fmtI64(tx.Amount)
		row.rightRef = tx.Reference
	}
	t.rows = append(t.rows, row)
	t.maybeWarn()
	return nil
}

func (t *tableWriter) WriteDuplicate(group DuplicateGroup) error {
	t.rows = append(t.rows, tableRow{
		typ:     "duplicate",
		leftRef: group.Reference,
		note:    "source=" + group.Source + " count=" + fmtInt(len(group.Transactions)),
	})
	t.maybeWarn()
	return nil
}

func (t *tableWriter) WriteSummary(s Summary) error {
	t.rows = append(t.rows, tableRow{
		typ: "summary",
		note: fmt.Sprintf("matched=%d unmatched_left=%d unmatched_right=%d rate=%.1f%% reconciled_rate=%.1f%%",
			s.MatchedCount, s.UnmatchedLeft, s.UnmatchedRight, s.MatchRatePct, s.ReconciledRatePct),
	})
	return nil
}

func (t *tableWriter) Flush() error {
	tw := tabwriter.NewWriter(t.w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "TYPE\tLEFT_ID\tLEFT_DATE\tLEFT_AMT\tLEFT_REF\tRIGHT_ID\tRIGHT_DATE\tRIGHT_AMT\tRIGHT_REF\tNOTE"); err != nil {
		return err
	}
	for _, row := range t.rows {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			row.typ, row.leftID, row.leftDt, row.leftAmt, row.leftRef,
			row.rightID, row.rightDt, row.rightAmt, row.rightRef, row.note); err != nil {
			return err
		}
	}
	return tw.Flush()
}
