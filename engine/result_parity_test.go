package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/reconifyhq/reconify/config"
)

func TestExecutionPathParity_SingleSourceRows(t *testing.T) {
	dir := t.TempDir()
	leftPath := filepath.Join(dir, "left.csv")
	rightPath := filepath.Join(dir, "right.csv")
	writeParityCSV(t, leftPath, []string{
		"2026-01-01,100,USD,MATCH,MATCH",
		"2026-01-01,200,USD,AMOUNT,AMOUNT",
		"2026-01-01,300,USD,TIMING,TIMING",
		"2026-01-01,400,USD,LEFT_ONLY,LEFT_ONLY",
		"2026-01-01,500,USD,DUP-ONE,DUPLICATE",
		"2026-01-01,600,USD,DUP-TWO,DUPLICATE",
	})
	writeParityCSV(t, rightPath, []string{
		"2026-01-01,100,USD,MATCH,MATCH",
		"2026-01-01,250,USD,AMOUNT,AMOUNT",
		"2026-01-05,300,USD,TIMING,TIMING",
		"2026-01-01,700,USD,RIGHT_ONLY,RIGHT_ONLY",
		"2026-01-01,500,USD,DUP-ONE,DUPLICATE",
		"2026-01-01,600,USD,DUP-TWO,DUPLICATE",
	})

	cfg := parityParserCfg()
	pair := config.Pair{DateWindow: "1d"}
	left := mustParse(t, "left", leftPath, cfg)
	right := mustParse(t, "right", rightPath, cfg)
	batch, err := Reconcile("parity", "left", "right", left, right, pair)
	if err != nil {
		t.Fatal(err)
	}
	batchJSON := marshalParityResult(t, batch)
	assertParitySummary(t, batch, 3, 1, 1, 1, 1)
	if batch.Summary.DuplicateCount != 4 {
		t.Fatalf("DuplicateCount=%d, want 4", batch.Summary.DuplicateCount)
	}
	assertExactRowCoverage(t, left, [][]Transaction{right}, batch)

	runStreaming := func(t *testing.T, name string, index RightIndex) {
		t.Helper()
		defer func() { _ = index.Close() }()
		gotJSON := runParityWriter(t, "left", "right", func(w *jsonWriter) error {
			return ReconcileStreaming(context.Background(), "parity", "left", "right", leftPath, rightPath, cfg, cfg, pair, index, w, 0)
		})
		got := decodeParityResult(t, gotJSON)
		assertJSONParity(t, name, batchJSON, gotJSON)
		assertExactRowCoverage(t, left, [][]Transaction{right}, &got)
	}

	t.Run("streaming_memory", func(t *testing.T) {
		runStreaming(t, "streaming memory", NewMemoryIndex())
	})
	t.Run("streaming_disk", func(t *testing.T) {
		index, err := NewDiskIndex(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		runStreaming(t, "streaming disk", index)
	})
	t.Run("partitioned", func(t *testing.T) {
		gotJSON := runParityWriter(t, "left", "right", func(w *jsonWriter) error {
			return ReconcilePartitioned(context.Background(), "parity", "left", "right", leftPath, rightPath, cfg, cfg, pair, w, 0, 3)
		})
		got := decodeParityResult(t, gotJSON)
		assertJSONParity(t, "partitioned", batchJSON, gotJSON)
		assertExactRowCoverage(t, left, [][]Transaction{right}, &got)
	})
}

func TestExecutionPathParity_GroupedRows(t *testing.T) {
	t.Run("one_to_many", func(t *testing.T) {
		dir := t.TempDir()
		leftPath := filepath.Join(dir, "left.csv")
		rightPath := filepath.Join(dir, "right.csv")
		writeParityCSV(t, leftPath, []string{
			"2026-01-01,300,USD,MATCH,MATCH",
			"2026-01-01,300,USD,AMOUNT,AMOUNT",
			"2026-01-01,300,USD,TIMING,TIMING",
			"2026-01-01,100,USD,AMBIG,AMBIG",
			"2026-01-01,150,USD,AMBIG,AMBIG",
			"2026-01-01,10,USD,LEFT_ONLY,LEFT_ONLY",
		})
		writeParityCSV(t, rightPath, []string{
			"2026-01-01,100,USD,MATCH,MATCH",
			"2026-01-01,200,USD,MATCH,MATCH",
			"2026-01-01,100,USD,AMOUNT,AMOUNT",
			"2026-01-01,100,USD,AMOUNT,AMOUNT",
			"2026-01-10,100,USD,TIMING,TIMING",
			"2026-01-10,200,USD,TIMING,TIMING",
			"2026-01-01,100,USD,AMBIG,AMBIG",
			"2026-01-01,5,USD,RIGHT_ONLY,RIGHT_ONLY",
		})

		cfg := parityParserCfg()
		cfg.GroupCol = ""
		pair := config.Pair{DateWindow: "1d", Passes: []config.PassConfig{{Type: config.PassTypeOneToMany}}}
		assertGroupedParity(t, leftPath, rightPath, cfg, pair)
	})

	t.Run("many_to_many", func(t *testing.T) {
		dir := t.TempDir()
		leftPath := filepath.Join(dir, "left.csv")
		rightPath := filepath.Join(dir, "right.csv")
		writeParityCSV(t, leftPath, []string{
			"2026-01-01,150,USD,MATCH,MATCH",
			"2026-01-01,150,USD,MATCH,MATCH",
			"2026-01-01,200,USD,AMOUNT,AMOUNT",
			"2026-01-01,100,USD,AMOUNT,AMOUNT",
			"2026-01-01,100,USD,TIMING,TIMING",
			"2026-01-01,200,USD,TIMING,TIMING",
			"2026-01-01,10,USD,LEFT_ONLY,LEFT_ONLY",
		})
		writeParityCSV(t, rightPath, []string{
			"2026-01-01,200,USD,MATCH,MATCH",
			"2026-01-01,100,USD,MATCH,MATCH",
			"2026-01-01,200,USD,AMOUNT,AMOUNT",
			"2026-01-01,50,USD,AMOUNT,AMOUNT",
			"2026-01-10,100,USD,TIMING,TIMING",
			"2026-01-10,200,USD,TIMING,TIMING",
			"2026-01-01,5,USD,RIGHT_ONLY,RIGHT_ONLY",
		})

		cfg := parityParserCfg()
		cfg.GroupCol = ""
		pair := config.Pair{DateWindow: "1d", Passes: []config.PassConfig{{Type: config.PassTypeManyToMany}}}
		assertGroupedParity(t, leftPath, rightPath, cfg, pair)
	})
}

func TestExecutionPathParity_MultiSourceRows(t *testing.T) {
	dir := t.TempDir()
	leftPath := filepath.Join(dir, "left.csv")
	firstPath := filepath.Join(dir, "first.csv")
	secondPath := filepath.Join(dir, "second.csv")
	writeParityCSV(t, leftPath, []string{
		"2026-01-01,100,USD,FIRST,FIRST",
		"2026-01-01,200,USD,SECOND,SECOND",
		"2026-01-01,300,USD,AMOUNT,AMOUNT",
		"2026-01-01,400,USD,TIMING,TIMING",
		"2026-01-01,500,USD,LEFT_ONLY,LEFT_ONLY",
	})
	writeParityCSV(t, firstPath, []string{
		"2026-01-01,100,USD,FIRST,FIRST",
		"2026-01-01,250,USD,AMOUNT,AMOUNT",
		"2026-01-05,400,USD,TIMING,TIMING",
		"2026-01-01,100,USD,FIRST,FIRST_AGAIN",
	})
	writeParityCSV(t, secondPath, []string{
		"2026-01-01,200,USD,SECOND,SECOND",
		"2026-01-01,5,USD,RIGHT_ONLY,RIGHT_ONLY",
	})

	cfg := parityParserCfg()
	cfg.GroupCol = ""
	cfg.CurrencyCol = ""
	pair := config.Pair{DateWindow: "1d"}
	left := mustParse(t, "left", leftPath, cfg)
	first := mustParse(t, "first", firstPath, cfg)
	second := mustParse(t, "second", secondPath, cfg)
	batch, err := ReconcileMultiSource("parity", "left", left, []CounterpartInput{
		{SourceName: "first", Transactions: first, ParserCfg: cfg},
		{SourceName: "second", Transactions: second, ParserCfg: cfg},
	}, pair)
	if err != nil {
		t.Fatal(err)
	}
	batchJSON := marshalParityResult(t, batch)
	assertParitySummary(t, batch, 2, 1, 1, 1, 2)
	assertMultiSourceBreakdown(t, batch)
	assertExactRowCoverage(t, left, [][]Transaction{first, second}, batch)

	t.Run("streaming", func(t *testing.T) {
		indexes := []RightIndex{NewMemoryIndex(), NewMemoryIndex()}
		defer func() {
			for _, index := range indexes {
				_ = index.Close()
			}
		}()
		gotJSON := runParityWriter(t, "left", "first,second", func(w *jsonWriter) error {
			return ReconcileStreamingMultiSource(context.Background(), "parity", "left", leftPath, cfg, []CounterpartStream{
				{SourceName: "first", RightPath: firstPath, RightCfg: cfg, Index: indexes[0]},
				{SourceName: "second", RightPath: secondPath, RightCfg: cfg, Index: indexes[1]},
			}, pair, w, 0)
		})
		got := decodeParityResult(t, gotJSON)
		assertJSONParity(t, "multi-source streaming", batchJSON, gotJSON)
		assertExactRowCoverage(t, left, [][]Transaction{first, second}, &got)
	})

	t.Run("partitioned", func(t *testing.T) {
		gotJSON := runParityWriter(t, "left", "first,second", func(w *jsonWriter) error {
			return ReconcilePartitionedMultiSourceWithOptions(context.Background(), "parity", "left", leftPath, cfg, []PartitionedCounterpartInput{
				{SourceName: "first", RightPath: firstPath, ParserCfg: cfg},
				{SourceName: "second", RightPath: secondPath, ParserCfg: cfg},
			}, pair, w, PartitionedOptions{Partitions: 3, SpillDir: filepath.Join(dir, "spill")})
		})
		got := decodeParityResult(t, gotJSON)
		assertJSONParity(t, "multi-source partitioned", batchJSON, gotJSON)
		assertExactRowCoverage(t, left, [][]Transaction{first, second}, &got)
	})
}

func assertGroupedParity(t *testing.T, leftPath, rightPath string, cfg config.ParserCfg, pair config.Pair) {
	t.Helper()
	left := mustParse(t, "left", leftPath, cfg)
	right := mustParse(t, "right", rightPath, cfg)
	batch, err := Reconcile("parity", "left", "right", left, right, pair)
	if err != nil {
		t.Fatal(err)
	}
	batchJSON := marshalParityResult(t, batch)
	assertSummaryMatchesCounts(t, batch)
	assertMonetaryInvariant(t, batch.Summary)
	assertGroupedOutcomes(t, batch, pair.Passes[0].Type)
	assertExactRowCoverage(t, left, [][]Transaction{right}, batch)

	gotJSON := runParityWriter(t, "left", "right", func(w *jsonWriter) error {
		return ReconcilePartitioned(context.Background(), "parity", "left", "right", leftPath, rightPath, cfg, cfg, pair, w, 0, 3)
	})
	got := decodeParityResult(t, gotJSON)
	assertJSONParity(t, "grouped partitioned", batchJSON, gotJSON)
	assertSummaryMatchesCounts(t, &got)
	assertMonetaryInvariant(t, got.Summary)
	assertExactRowCoverage(t, left, [][]Transaction{right}, &got)
}

func parityParserCfg() config.ParserCfg {
	return config.ParserCfg{
		Type: "csv", DateCol: "date", DateLayout: "2006-01-02", AmountCol: "amount",
		CurrencyCol: "currency", RefCol: "reference", GroupCol: "group", SkipRaw: true,
	}
}

func writeParityCSV(t *testing.T, path string, rows []string) {
	t.Helper()
	content := "date,amount,currency,reference,group\n" + strings.Join(rows, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func marshalParityResult(t *testing.T, result *Result) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := newJSONWriter(&output)
	writer.SetMeta(result.PairName, result.LeftSource, result.RightSource)
	if err := WriteResult(writer, result); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func runParityWriter(t *testing.T, leftSource, rightSource string, run func(*jsonWriter) error) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := newJSONWriter(&output)
	writer.SetMeta("parity", leftSource, rightSource)
	if err := run(writer); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func decodeParityResult(t *testing.T, data []byte) Result {
	t.Helper()
	var result Result
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func assertJSONParity(t *testing.T, path string, baseline, got []byte) {
	t.Helper()
	baseCanonical := canonicalParityJSON(t, baseline)
	gotCanonical := canonicalParityJSON(t, got)
	if baseCanonical != gotCanonical {
		t.Fatalf("%s result mismatch\nbaseline: %s\nactual:   %s", path, baseCanonical, gotCanonical)
	}
}

func canonicalParityJSON(t *testing.T, data []byte) string {
	t.Helper()
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	normalizeParityJSON(value)
	sortJSONArrays(value)
	canonical, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(canonical)
}

// normalizeParityJSON removes GroupKey because RightIndex stores only the fields
// required for matching and does not retain it on rows emitted by streaming paths.
// The tests still compare every outcome bucket, transaction identity, monetary
// value, date, reference, source, duplicate annotation, and summary field.
func normalizeParityJSON(value any) {
	switch current := value.(type) {
	case map[string]any:
		delete(current, "group_key")
		for _, child := range current {
			normalizeParityJSON(child)
		}
	case []any:
		for _, child := range current {
			normalizeParityJSON(child)
		}
	}
}

func assertExactRowCoverage(t *testing.T, left []Transaction, rightSets [][]Transaction, result *Result) {
	t.Helper()
	expected := make(map[string]string, len(left))
	for _, tx := range left {
		expected[parityRowKey(tx)] = "left"
	}
	for _, right := range rightSets {
		for _, tx := range right {
			expected[parityRowKey(tx)] = "right"
		}
	}

	seen := make(map[string]int, len(expected))
	record := func(tx Transaction, side string) {
		key := parityRowKey(tx)
		if wantSide, ok := expected[key]; !ok {
			t.Errorf("row %q emitted on %s but is not an input row", key, side)
		} else if wantSide != side {
			t.Errorf("row %q emitted on %s, want %s", key, side, wantSide)
		}
		seen[key]++
	}

	for _, pair := range result.Matched {
		record(pair.Left, "left")
		record(pair.Right, "right")
	}
	for _, pair := range result.AmountDiff {
		record(pair.Left, "left")
		record(pair.Right, "right")
	}
	for _, pair := range result.TimingDiff {
		record(pair.Left, "left")
		record(pair.Right, "right")
	}
	for _, tx := range result.UnmatchedLeft {
		record(tx, "left")
	}
	for _, tx := range result.UnmatchedRight {
		record(tx, "right")
	}
	for _, pair := range result.GroupedMatched {
		record(pair.Left, "left")
		for _, tx := range pair.Rights {
			record(tx, "right")
		}
	}
	for _, pair := range result.GroupedAmountDiff {
		record(pair.Left, "left")
		for _, tx := range pair.Rights {
			record(tx, "right")
		}
	}
	for _, pair := range result.GroupedTimingDiff {
		record(pair.Left, "left")
		for _, tx := range pair.Rights {
			record(tx, "right")
		}
	}
	for _, pair := range result.AmbiguousGroups {
		for _, tx := range pair.LeftRows {
			record(tx, "left")
		}
		for _, tx := range pair.Rights {
			record(tx, "right")
		}
	}
	for _, pair := range result.ManyToManyMatched {
		recordManyToManyRows(record, pair.Lefts, pair.Rights)
	}
	for _, pair := range result.ManyToManyAmountDiff {
		recordManyToManyRows(record, pair.Lefts, pair.Rights)
	}
	for _, pair := range result.ManyToManyTimingDiff {
		recordManyToManyRows(record, pair.Lefts, pair.Rights)
	}

	for key := range expected {
		if seen[key] != 1 {
			t.Errorf("row %q emitted %d times, want exactly once", key, seen[key])
		}
	}
}

func recordManyToManyRows(record func(Transaction, string), left, right []Transaction) {
	for _, tx := range left {
		record(tx, "left")
	}
	for _, tx := range right {
		record(tx, "right")
	}
}

func parityRowKey(tx Transaction) string {
	return tx.Source + ":" + tx.ID
}

func assertParitySummary(t *testing.T, result *Result, matched, amountDiff, timingDiff, unmatchedLeft, unmatchedRight int) {
	t.Helper()
	if result.Summary.MatchedCount != matched || result.Summary.AmountDiffCount != amountDiff ||
		result.Summary.TimingDiffCount != timingDiff || result.Summary.UnmatchedLeft != unmatchedLeft ||
		result.Summary.UnmatchedRight != unmatchedRight {
		t.Fatalf("summary=%+v, want matched=%d amount_diff=%d timing_diff=%d unmatched_left=%d unmatched_right=%d",
			result.Summary, matched, amountDiff, timingDiff, unmatchedLeft, unmatchedRight)
	}
}

func assertGroupedOutcomes(t *testing.T, result *Result, passType string) {
	t.Helper()
	if result.Summary.UnmatchedLeft != 1 || result.Summary.UnmatchedRight != 1 {
		t.Fatalf("grouped summary=%+v, want one unmatched row on each side", result.Summary)
	}
	switch passType {
	case config.PassTypeOneToMany:
		if result.Summary.GroupedMatchedCount != 1 || result.Summary.GroupedAmountDiffCount != 1 ||
			result.Summary.GroupedTimingDiffCount != 1 || result.Summary.AmbiguousGroupCount != 1 {
			t.Fatalf("one-to-many summary=%+v, want one match, amount diff, timing diff, and ambiguous group", result.Summary)
		}
	case config.PassTypeManyToMany:
		if result.Summary.ManyToManyMatchedCount != 1 || result.Summary.ManyToManyAmountDiffCount != 1 ||
			result.Summary.ManyToManyTimingDiffCount != 1 {
			t.Fatalf("many-to-many summary=%+v, want one match, amount diff, and timing diff", result.Summary)
		}
	default:
		t.Fatalf("unsupported grouped parity pass %q", passType)
	}
}

func assertMultiSourceBreakdown(t *testing.T, result *Result) {
	t.Helper()
	first, firstOK := result.BySource["first"]
	second, secondOK := result.BySource["second"]
	if !firstOK || !secondOK {
		t.Fatalf("BySource=%+v, want first and second summaries", result.BySource)
	}
	if first.MatchedCount != 1 || first.AmountDiffCount != 1 || first.TimingDiffCount != 1 || first.UnmatchedRight != 1 {
		t.Fatalf("first summary=%+v, want match, amount diff, timing diff, and one unmatched right", first)
	}
	if second.MatchedCount != 1 || second.UnmatchedRight != 1 || second.UnmatchedLeft != 1 {
		t.Fatalf("second summary=%+v, want match and one unmatched row on each side", second)
	}
}
