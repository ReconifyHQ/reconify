package engine

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
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
// Byte-identical to current reconcile output. Not streaming-friendly for large files.
// ---------------------------------------------------------------------------

type jsonWriter struct {
	w      io.Writer
	result Result
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
func (j *jsonWriter) Flush() error {
	enc := json.NewEncoder(j.w)
	enc.SetIndent("", "  ")
	return enc.Encode(j.result)
}

// GetResult returns the accumulated Result. Useful for callers that need the
// struct directly (e.g. the Reconcile wrapper).
func (j *jsonWriter) GetResult() *Result { return &j.result }

// ---------------------------------------------------------------------------
// JSONStreamWriter — buffers JSON bytes per section; immediate encoding on
// each event. More GC-friendly than JSONWriter since Go structs are released
// after encoding. Structurally identical output to JSONWriter.
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
	sections []jsonStreamSection // ordered; keyed by JSON field name
	byKey    map[string]*jsonStreamSection
	summary  *Summary
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
func (j *jsonStreamWriter) Flush() error {
	// Build a result-shaped object from accumulated raw messages.
	// All sections are always present; missing ones output as empty arrays.
	result := map[string]any{
		"pair":         j.meta.PairName,
		"left_source":  j.meta.LeftSource,
		"right_source": j.meta.RightSource,
	}
	if j.summary != nil {
		result["summary"] = j.summary
	} else {
		result["summary"] = Summary{}
	}
	for _, sec := range j.sections {
		if len(sec.events) == 0 {
			result[sec.key] = []json.RawMessage{}
		} else {
			result[sec.key] = sec.events
		}
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

// ---------------------------------------------------------------------------
// NDJSONWriter — one tagged JSON envelope per event, immediately written.
// O(1) memory. Crash-safe: each line is independently valid JSON.
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

func (n *ndjsonWriter) WriteMatch(pair MatchedPair) error        { return n.emit("match", pair) }
func (n *ndjsonWriter) WriteAmountDiff(pair AmountDiffPair) error { return n.emit("amount_diff", pair) }
func (n *ndjsonWriter) WriteTimingDiff(pair TimingDiffPair) error { return n.emit("timing_diff", pair) }
func (n *ndjsonWriter) WriteUnmatched(tx Transaction, side string) error {
	return n.emit("unmatched_"+side, tx)
}
func (n *ndjsonWriter) WriteDuplicate(group DuplicateGroup) error { return n.emit("duplicate", group) }
func (n *ndjsonWriter) WriteSummary(s Summary) error              { return n.emit("summary", s) }
func (n *ndjsonWriter) Flush() error                              { return nil }

// ---------------------------------------------------------------------------
// CSVWriter — fixed schema, one row per event. O(1) memory. Versioned contract.
//
// Column order:
//   type, left_id, left_date, left_amount_minor, left_ref, left_name,
//   right_id, right_date, right_amount_minor, right_ref, right_name,
//   diff_minor, days_diff,
//   source, reference, dup_count,
//   total_left, total_right, matched, unmatched_left, unmatched_right,
//   amount_diff_count, timing_diff_count, duplicate_count, match_rate_pct
//
// Unused columns for a given event type are empty string.
// ---------------------------------------------------------------------------

var csvHeader = []string{
	"type",
	"left_id", "left_date", "left_amount_minor", "left_ref", "left_name",
	"right_id", "right_date", "right_amount_minor", "right_ref", "right_name",
	"diff_minor", "days_diff",
	"source", "reference", "dup_count",
	"total_left", "total_right", "matched", "unmatched_left", "unmatched_right",
	"amount_diff_count", "timing_diff_count", "duplicate_count", "match_rate_pct",
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

func txLeft(row []string, tx Transaction) {
	row[1] = tx.ID
	row[2] = fmtDate(tx.Date)
	row[3] = fmtI64(tx.Amount)
	row[4] = tx.Reference
	row[5] = tx.Name
}

func txRight(row []string, tx Transaction) {
	row[6] = tx.ID
	row[7] = fmtDate(tx.Date)
	row[8] = fmtI64(tx.Amount)
	row[9] = tx.Reference
	row[10] = tx.Name
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
	row[14] = group.Reference
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
	return c.w.Write(row)
}

func (c *csvWriter) Flush() error {
	c.w.Flush()
	return c.w.Error()
}

// ---------------------------------------------------------------------------
// TableWriter — buffers all rows, renders ASCII table via text/tabwriter on Flush().
// Not suitable for large datasets: warns at tableWarnThreshold rows.
// ---------------------------------------------------------------------------

const tableWarnThreshold = 10_000

type tableRow struct {
	typ    string
	leftID string
	leftDt string
	leftAmt string
	leftRef string
	rightID string
	rightDt string
	rightAmt string
	rightRef string
	note    string
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
		fmt.Fprintf(t.w, "warning: table mode has buffered >%d rows; for large files use --format=ndjson or --format=csv\n", tableWarnThreshold)
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
		typ:     "unmatched_" + side,
		note:    "",
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
		typ:  "summary",
		note: fmt.Sprintf("matched=%d unmatched_left=%d unmatched_right=%d rate=%.1f%%", s.MatchedCount, s.UnmatchedLeft, s.UnmatchedRight, s.MatchRatePct),
	})
	return nil
}

func (t *tableWriter) Flush() error {
	tw := tabwriter.NewWriter(t.w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TYPE\tLEFT_ID\tLEFT_DATE\tLEFT_AMT\tLEFT_REF\tRIGHT_ID\tRIGHT_DATE\tRIGHT_AMT\tRIGHT_REF\tNOTE")
	for _, row := range t.rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			row.typ, row.leftID, row.leftDt, row.leftAmt, row.leftRef,
			row.rightID, row.rightDt, row.rightAmt, row.rightRef, row.note)
	}
	return tw.Flush()
}
