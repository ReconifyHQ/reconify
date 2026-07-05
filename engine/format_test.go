package engine

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// makeRunInfo returns a minimal RunInfo for test fixtures.
func makeRunInfo() RunInfo {
	return RunInfo{
		RunID:       "abcdef1234567890",
		Timestamp:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		ToolVersion: "v1.0.0",
		LeftFile:    FileInfo{Path: "left.csv", SHA256: strings.Repeat("a", 64)},
		RightFile:   FileInfo{Path: "right.csv", SHA256: strings.Repeat("b", 64)},
		PairConfig:  PairConfigSnap{DateWindow: "1d", AmountToleranceMinor: 0, NameMode: "exact"},
	}
}

func requireNoFormatErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// -----------------------------------------------------------------------------
// jsonWriter
// -----------------------------------------------------------------------------

func TestJSONWriter_SetMeta(t *testing.T) {
	var buf bytes.Buffer
	w := newJSONWriter(&buf)
	w.SetMeta("my_pair", "left_src", "right_src")
	requireNoFormatErr(t, w.WriteSummary(Summary{}))
	requireNoFormatErr(t, w.Flush())

	var result Result
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if result.PairName != "my_pair" {
		t.Errorf("PairName = %q, want %q", result.PairName, "my_pair")
	}
	if result.LeftSource != "left_src" {
		t.Errorf("LeftSource = %q, want %q", result.LeftSource, "left_src")
	}
	if result.RightSource != "right_src" {
		t.Errorf("RightSource = %q, want %q", result.RightSource, "right_src")
	}
}

func TestJSONWriter_SetRunInfo(t *testing.T) {
	var buf bytes.Buffer
	w := newJSONWriter(&buf)
	info := makeRunInfo()
	if err := w.SetRunInfo(info); err != nil {
		t.Fatalf("SetRunInfo: %v", err)
	}
	requireNoFormatErr(t, w.WriteSummary(Summary{}))
	requireNoFormatErr(t, w.Flush())

	var result Result
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if result.RunInfo == nil {
		t.Fatal("RunInfo is nil after SetRunInfo")
	}
	if result.RunInfo.RunID != info.RunID {
		t.Errorf("RunInfo.RunID = %q, want %q", result.RunInfo.RunID, info.RunID)
	}
}

func TestJSONWriter_NoRunInfoByDefault(t *testing.T) {
	var buf bytes.Buffer
	w := newJSONWriter(&buf)
	requireNoFormatErr(t, w.WriteSummary(Summary{}))
	requireNoFormatErr(t, w.Flush())

	var result Result
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if result.RunInfo != nil {
		t.Error("RunInfo should be nil when SetRunInfo was not called")
	}
}

func TestJSONWriter_SetDeterministic_StableOutput(t *testing.T) {
	buildResult := func() string {
		var buf bytes.Buffer
		w := newJSONWriter(&buf)
		w.SetDeterministic(true)
		// Insert matches in reverse order so sorting is meaningful.
		requireNoFormatErr(t, w.WriteMatch(MatchedPair{
			Left:  Transaction{ID: "left-b"},
			Right: Transaction{ID: "right-b"},
		}))
		requireNoFormatErr(t, w.WriteMatch(MatchedPair{
			Left:  Transaction{ID: "left-a"},
			Right: Transaction{ID: "right-a"},
		}))
		requireNoFormatErr(t, w.WriteUnmatched(Transaction{ID: "ul-z"}, "left"))
		requireNoFormatErr(t, w.WriteUnmatched(Transaction{ID: "ul-a"}, "left"))
		requireNoFormatErr(t, w.WriteSummary(Summary{}))
		requireNoFormatErr(t, w.Flush())
		return buf.String()
	}

	out1 := buildResult()
	out2 := buildResult()
	if out1 != out2 {
		t.Error("SetDeterministic output is not stable across two builds")
	}

	// Verify sort order: left-a must appear before left-b in matched.
	idxA := strings.Index(out1, `"left-a"`)
	idxB := strings.Index(out1, `"left-b"`)
	if idxA >= idxB {
		t.Errorf("expected left-a before left-b in sorted output")
	}
}

func TestJSONWriter_ManyToManyEvents(t *testing.T) {
	var buf bytes.Buffer
	w := newJSONWriter(&buf)
	requireNoFormatErr(t, w.WriteManyToManyMatch(ManyToManyMatchedPair{
		Lefts:  []Transaction{{ID: "l1"}, {ID: "l2"}},
		Rights: []Transaction{{ID: "r1"}},
	}))
	requireNoFormatErr(t, w.WriteManyToManyAmountDiff(ManyToManyAmountDiffPair{
		Lefts:     []Transaction{{ID: "l3"}},
		Rights:    []Transaction{{ID: "r2"}, {ID: "r3"}},
		DiffMinor: 5,
	}))
	requireNoFormatErr(t, w.WriteManyToManyTimingDiff(ManyToManyTimingDiffPair{
		Lefts:    []Transaction{{ID: "l4"}},
		Rights:   []Transaction{{ID: "r4"}},
		DaysDiff: 3,
	}))
	requireNoFormatErr(t, w.WriteSummary(Summary{}))
	requireNoFormatErr(t, w.Flush())

	var result Result
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if len(result.ManyToManyMatched) != 1 {
		t.Fatalf("ManyToManyMatched=%d want 1", len(result.ManyToManyMatched))
	}
	if len(result.ManyToManyAmountDiff) != 1 || result.ManyToManyAmountDiff[0].DiffMinor != 5 {
		t.Fatalf("ManyToManyAmountDiff=%+v, want one diff_minor=5", result.ManyToManyAmountDiff)
	}
	if len(result.ManyToManyTimingDiff) != 1 || result.ManyToManyTimingDiff[0].DaysDiff != 3 {
		t.Fatalf("ManyToManyTimingDiff=%+v, want one days_diff=3", result.ManyToManyTimingDiff)
	}
}

// -----------------------------------------------------------------------------
// jsonStreamWriter
// -----------------------------------------------------------------------------

func TestJSONStreamWriter_SetRunInfo(t *testing.T) {
	var buf bytes.Buffer
	w := newJSONStreamWriter(&buf)
	info := makeRunInfo()
	if err := w.SetRunInfo(info); err != nil {
		t.Fatalf("SetRunInfo: %v", err)
	}
	requireNoFormatErr(t, w.WriteSummary(Summary{}))
	requireNoFormatErr(t, w.Flush())

	var result map[string]json.RawMessage
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if _, ok := result["run_info"]; !ok {
		t.Fatal("run_info key missing from json-stream output")
	}
}

func TestJSONStreamWriter_NoRunInfoByDefault(t *testing.T) {
	var buf bytes.Buffer
	w := newJSONStreamWriter(&buf)
	requireNoFormatErr(t, w.WriteSummary(Summary{}))
	requireNoFormatErr(t, w.Flush())

	var result map[string]json.RawMessage
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if _, ok := result["run_info"]; ok {
		t.Error("run_info present in json-stream output when SetRunInfo was not called")
	}
}

func TestJSONStreamWriter_ManyToManyEvents(t *testing.T) {
	var buf bytes.Buffer
	w := newJSONStreamWriter(&buf)
	requireNoFormatErr(t, w.WriteManyToManyMatch(ManyToManyMatchedPair{
		Lefts:  []Transaction{{ID: "l1"}},
		Rights: []Transaction{{ID: "r1"}},
	}))
	requireNoFormatErr(t, w.WriteManyToManyAmountDiff(ManyToManyAmountDiffPair{
		Lefts:     []Transaction{{ID: "l2"}},
		Rights:    []Transaction{{ID: "r2"}},
		DiffMinor: 7,
	}))
	requireNoFormatErr(t, w.WriteManyToManyTimingDiff(ManyToManyTimingDiffPair{
		Lefts:    []Transaction{{ID: "l3"}},
		Rights:   []Transaction{{ID: "r3"}},
		DaysDiff: 4,
	}))
	requireNoFormatErr(t, w.WriteSummary(Summary{}))
	requireNoFormatErr(t, w.Flush())

	var result map[string]json.RawMessage
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	for _, key := range []string{keyManyToManyMatched, keyManyToManyAmountDiff, keyManyToManyTimingDiff} {
		if _, ok := result[key]; !ok {
			t.Fatalf("%s key missing from json-stream output", key)
		}
	}
}

// -----------------------------------------------------------------------------
// ndjsonWriter
// -----------------------------------------------------------------------------

func TestNDJSONWriter_SetRunInfo_FirstLine(t *testing.T) {
	var buf bytes.Buffer
	w := newNDJSONWriter(&buf)
	info := makeRunInfo()
	if err := w.SetRunInfo(info); err != nil {
		t.Fatalf("SetRunInfo: %v", err)
	}
	// Emit some events after run_info
	requireNoFormatErr(t, w.WriteMatch(MatchedPair{Left: Transaction{ID: "l1"}, Right: Transaction{ID: "r1"}}))
	requireNoFormatErr(t, w.WriteSummary(Summary{}))
	requireNoFormatErr(t, w.Flush())

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 lines, got %d", len(lines))
	}

	var firstEnvelope ndjsonEnvelope
	if err := json.Unmarshal([]byte(lines[0]), &firstEnvelope); err != nil {
		t.Fatalf("unmarshal first line: %v", err)
	}
	if firstEnvelope.Type != "run_info" {
		t.Errorf("first line type = %q, want %q", firstEnvelope.Type, "run_info")
	}
}

func TestNDJSONWriter_EachLineValidJSON(t *testing.T) {
	var buf bytes.Buffer
	w := newNDJSONWriter(&buf)
	requireNoFormatErr(t, w.SetRunInfo(makeRunInfo()))
	requireNoFormatErr(t, w.WriteMatch(MatchedPair{Left: Transaction{ID: "l1"}, Right: Transaction{ID: "r1"}}))
	requireNoFormatErr(t, w.WriteUnmatched(Transaction{ID: "ul1"}, "left"))
	requireNoFormatErr(t, w.WriteSummary(Summary{TotalLeft: 2, TotalRight: 2}))
	requireNoFormatErr(t, w.Flush())

	for i, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if !json.Valid([]byte(line)) {
			t.Errorf("line %d is not valid JSON: %q", i, line)
		}
	}
}

func TestNDJSONWriter_ManyToManyEvents(t *testing.T) {
	var buf bytes.Buffer
	w := newNDJSONWriter(&buf)
	requireNoFormatErr(t, w.WriteManyToManyMatch(ManyToManyMatchedPair{
		Lefts:  []Transaction{{ID: "l1"}},
		Rights: []Transaction{{ID: "r1"}},
	}))
	requireNoFormatErr(t, w.WriteManyToManyAmountDiff(ManyToManyAmountDiffPair{
		Lefts:     []Transaction{{ID: "l2"}},
		Rights:    []Transaction{{ID: "r2"}},
		DiffMinor: 9,
	}))
	requireNoFormatErr(t, w.WriteManyToManyTimingDiff(ManyToManyTimingDiffPair{
		Lefts:    []Transaction{{ID: "l3"}},
		Rights:   []Transaction{{ID: "r3"}},
		DaysDiff: 6,
	}))
	requireNoFormatErr(t, w.Flush())

	gotTypes := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var env ndjsonEnvelope
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			t.Fatalf("unmarshal line: %v", err)
		}
		gotTypes[env.Type] = true
	}
	for _, typ := range []string{eventManyToManyMatch, keyManyToManyAmountDiff, keyManyToManyTimingDiff} {
		if !gotTypes[typ] {
			t.Fatalf("missing ndjson event type %q in %v", typ, gotTypes)
		}
	}
}

// -----------------------------------------------------------------------------
// csvWriter — must NOT implement RunInfoSetter
// -----------------------------------------------------------------------------

func TestCSVWriter_DoesNotImplementRunInfoSetter(t *testing.T) {
	w := newCSVWriter(bytes.NewBuffer(nil))
	var rw ResultWriter = w
	if _, ok := rw.(RunInfoSetter); ok {
		t.Error("csvWriter should not implement RunInfoSetter")
	}
}

func TestCSVWriter_SummaryIncludesMonetaryTotals(t *testing.T) {
	var buf bytes.Buffer
	w := newCSVWriter(&buf)
	if err := w.WriteSummary(Summary{
		TotalLeft:            10,
		TotalRight:           9,
		MatchedCount:         7,
		UnmatchedLeft:        2,
		UnmatchedRight:       1,
		AmountDiffCount:      1,
		TimingDiffCount:      0,
		DuplicateCount:       0,
		MatchRatePct:         70.0,
		MatchedAmountLeft:    1200,
		MatchedAmountRight:   1200,
		UnmatchedAmountLeft:  250,
		UnmatchedAmountRight: 80,
		AmountDiffTotal:      20,
		TotalDiscrepancy:     350,
	}); err != nil {
		t.Fatalf("WriteSummary: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	rows, err := csv.NewReader(strings.NewReader(buf.String())).ReadAll()
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows=%d, want 2", len(rows))
	}
	header := rows[0]
	summary := rows[1]
	idx := map[string]int{}
	for i, col := range header {
		idx[col] = i
	}
	for _, col := range []string{
		"matched_amount_left", "matched_amount_right", "unmatched_amount_left",
		"unmatched_amount_right", "amount_diff_total", "total_discrepancy",
	} {
		if _, ok := idx[col]; !ok {
			t.Fatalf("missing column %q in CSV header", col)
		}
	}
	if summary[idx["matched_amount_left"]] != "1200" {
		t.Errorf("matched_amount_left=%q, want 1200", summary[idx["matched_amount_left"]])
	}
	if summary[idx["total_discrepancy"]] != "350" {
		t.Errorf("total_discrepancy=%q, want 350", summary[idx["total_discrepancy"]])
	}
}

func TestSanitizeCSVField(t *testing.T) {
	tests := map[string]string{
		"":         "",
		"safe":     "safe",
		"=SUM(A1)": "'=SUM(A1)",
		"+SUM(A1)": "'+SUM(A1)",
		"-SUM(A1)": "'-SUM(A1)",
		"@cmd":     "'@cmd",
		"\t=cmd":   "'\t=cmd",
		"\r=cmd":   "'\r=cmd",
	}
	for in, want := range tests {
		if got := SanitizeCSVField(in); got != want {
			t.Errorf("SanitizeCSVField(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCSVWriter_SanitizesFormulaFields(t *testing.T) {
	var buf bytes.Buffer
	w := newCSVWriter(&buf)
	if err := w.WriteMatch(MatchedPair{
		Left: Transaction{
			ID:        "left-1",
			Date:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			Reference: "=SUM(A1:A2)",
			Name:      "@malicious",
		},
		Right: Transaction{
			ID:        "right-1",
			Date:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			Reference: "+SUM(A1:A2)",
			Name:      "-malicious",
		},
	}); err != nil {
		t.Fatalf("WriteMatch: %v", err)
	}
	if err := w.WriteDuplicate(DuplicateGroup{Source: "left", Reference: "=DUP", Transactions: []Transaction{{ID: "left-1"}}}); err != nil {
		t.Fatalf("WriteDuplicate: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	rows, err := csv.NewReader(strings.NewReader(buf.String())).ReadAll()
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	if rows[1][4] != "'=SUM(A1:A2)" || rows[1][5] != "'@malicious" || rows[1][9] != "'+SUM(A1:A2)" || rows[1][10] != "'-malicious" {
		t.Fatalf("match row was not sanitized: %#v", rows[1])
	}
	if rows[2][14] != "'=DUP" {
		t.Fatalf("duplicate reference was not sanitized: %#v", rows[2])
	}
}

// -----------------------------------------------------------------------------
// tableWriter — must NOT implement RunInfoSetter
// -----------------------------------------------------------------------------

func TestTableWriter_DoesNotImplementRunInfoSetter(t *testing.T) {
	w := newTableWriter(bytes.NewBuffer(nil))
	var rw ResultWriter = w
	if _, ok := rw.(RunInfoSetter); ok {
		t.Error("tableWriter should not implement RunInfoSetter")
	}
}

// -----------------------------------------------------------------------------
// NewResultWriter factory
// -----------------------------------------------------------------------------

func TestNewResultWriter_InvalidFormat(t *testing.T) {
	_, err := NewResultWriter("xml", bytes.NewBuffer(nil))
	if err == nil {
		t.Error("expected error for unknown format, got nil")
	}
}

func TestNewResultWriter_AllValidFormats(t *testing.T) {
	for _, fmt := range []string{"json", "json-stream", "ndjson", "csv", "table"} {
		_, err := NewResultWriter(fmt, bytes.NewBuffer(nil))
		if err != nil {
			t.Errorf("NewResultWriter(%q) error: %v", fmt, err)
		}
	}
}
