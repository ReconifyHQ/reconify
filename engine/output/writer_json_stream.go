//nolint:staticcheck // Domain aliases keep package-internal signatures readable.
package output

import (
	"encoding/json"
	"io"

	. "github.com/reconifyhq/reconify/engine/domain"
)

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
	w              io.Writer
	meta           struct{ PairName, LeftSource, RightSource string }
	runInfo        *RunInfo
	indexSelection *IndexSelection
	sections       []jsonStreamSection // ordered; keyed by JSON field name
	byKey          map[string]*jsonStreamSection
	summary        *Summary
	bySource       map[string]Summary
	// grouped/ambiguous events from one_to_many pass — only emitted when non-empty
	groupedMatched       []json.RawMessage
	groupedAmountDiff    []json.RawMessage
	groupedTimingDiff    []json.RawMessage
	ambiguousGroups      []json.RawMessage
	manyToManyMatched    []json.RawMessage
	manyToManyAmountDiff []json.RawMessage
	manyToManyTimingDiff []json.RawMessage
	// subset_sum events — only emitted when non-empty
	subsetSumMatched   []json.RawMessage
	subsetSumAmbiguous []json.RawMessage
	subsetSumSkipped   []json.RawMessage
}

func newJSONStreamWriter(w io.Writer) *jsonStreamWriter {
	order := []string{"matched", "unmatched_left", "unmatched_right", "amount_diff", "timing_diff", "duplicates", "financial_effect_match", "financial_effect_diff", "financial_unchecked", "settlement_match", "settlement_diff"}
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

// ManyToManyEventWriter implementation — buffers encoded JSON; emitted in Flush() only when non-empty.
func (j *jsonStreamWriter) WriteManyToManyMatch(pair ManyToManyMatchedPair) error {
	b, err := j.encode(pair)
	if err != nil {
		return err
	}
	j.manyToManyMatched = append(j.manyToManyMatched, b)
	return nil
}
func (j *jsonStreamWriter) WriteManyToManyAmountDiff(pair ManyToManyAmountDiffPair) error {
	b, err := j.encode(pair)
	if err != nil {
		return err
	}
	j.manyToManyAmountDiff = append(j.manyToManyAmountDiff, b)
	return nil
}
func (j *jsonStreamWriter) WriteManyToManyTimingDiff(pair ManyToManyTimingDiffPair) error {
	b, err := j.encode(pair)
	if err != nil {
		return err
	}
	j.manyToManyTimingDiff = append(j.manyToManyTimingDiff, b)
	return nil
}

// SubsetSumEventWriter implementation — buffers encoded JSON; emitted in Flush() only when non-empty.
func (j *jsonStreamWriter) WriteSubsetSumMatch(pair SubsetSumMatchedPair) error {
	b, err := j.encode(pair)
	if err != nil {
		return err
	}
	j.subsetSumMatched = append(j.subsetSumMatched, b)
	return nil
}
func (j *jsonStreamWriter) WriteSubsetSumAmbiguous(pair SubsetSumAmbiguousPair) error {
	b, err := j.encode(pair)
	if err != nil {
		return err
	}
	j.subsetSumAmbiguous = append(j.subsetSumAmbiguous, b)
	return nil
}
func (j *jsonStreamWriter) WriteSubsetSumSkipped(pair SubsetSumSkippedPair) error {
	b, err := j.encode(pair)
	if err != nil {
		return err
	}
	j.subsetSumSkipped = append(j.subsetSumSkipped, b)
	return nil
}
func (j *jsonStreamWriter) WriteFinancialEffectMatch(f FinancialEffectFinding) error {
	return j.appendTo("financial_effect_match", f)
}
func (j *jsonStreamWriter) WriteFinancialEffectDiff(f FinancialEffectFinding) error {
	return j.appendTo("financial_effect_diff", f)
}
func (j *jsonStreamWriter) WriteFinancialUnchecked(f FinancialEffectFinding) error {
	return j.appendTo("financial_unchecked", f)
}
func (j *jsonStreamWriter) WriteSettlementMatch(f SettlementFinding) error {
	return j.appendTo("settlement_match", f)
}
func (j *jsonStreamWriter) WriteSettlementDiff(f SettlementFinding) error {
	return j.appendTo("settlement_diff", f)
}

func (j *jsonStreamWriter) Flush() error {
	result := map[string]any{
		"schema":       ResultSchemaV1,
		"pair":         j.meta.PairName,
		"left_source":  j.meta.LeftSource,
		"right_source": j.meta.RightSource,
	}
	if j.runInfo != nil {
		result["run_info"] = j.runInfo
	}
	if j.indexSelection != nil {
		result["index_selection"] = j.indexSelection
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
	if len(j.manyToManyMatched) > 0 {
		result[keyManyToManyMatched] = j.manyToManyMatched
	}
	if len(j.manyToManyAmountDiff) > 0 {
		result[keyManyToManyAmountDiff] = j.manyToManyAmountDiff
	}
	if len(j.manyToManyTimingDiff) > 0 {
		result[keyManyToManyTimingDiff] = j.manyToManyTimingDiff
	}
	// Subset-sum sections — only emitted when non-empty.
	if len(j.subsetSumMatched) > 0 {
		result[keySubsetSumMatched] = j.subsetSumMatched
	}
	if len(j.subsetSumAmbiguous) > 0 {
		result[keySubsetSumAmbiguous] = j.subsetSumAmbiguous
	}
	if len(j.subsetSumSkipped) > 0 {
		result[keySubsetSumSkipped] = j.subsetSumSkipped
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

func (j *jsonStreamWriter) SetIndexSelection(selection IndexSelection) error {
	j.indexSelection = &selection
	return nil
}
