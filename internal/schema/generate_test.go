package schema

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	. "github.com/reconifyhq/reconify/engine/domain"
	"github.com/reconifyhq/reconify/schemas"
)

func TestPublishedResultSchemaHasNoDrift(t *testing.T) {
	generated, err := GenerateResultSchemaJSON()
	if err != nil {
		t.Fatal(err)
	}
	if got := schemas.ResultV1(); !reflect.DeepEqual(generated, got) {
		t.Fatalf("published schema drifted from generator (generated %d bytes, published %d bytes)", len(generated), len(got))
	}
}

func TestPublishedResultSchemaValidatesJSONAndNDJSON(t *testing.T) {
	var document jsonschema.Schema
	if err := json.Unmarshal(schemas.ResultV1(), &document); err != nil {
		t.Fatalf("unmarshal published schema: %v", err)
	}
	resolved, err := document.Resolve(nil)
	if err != nil {
		t.Fatalf("resolve published schema: %v", err)
	}

	result := Result{
		Schema:         ResultSchemaV1,
		Matched:        []MatchedPair{},
		UnmatchedLeft:  []Transaction{},
		UnmatchedRight: []Transaction{},
		AmountDiff:     []AmountDiffPair{},
		TimingDiff:     []TimingDiffPair{},
		Duplicates:     []DuplicateGroup{},
	}
	validateJSON(t, resolved, result)

	events := []struct {
		typ  string
		data any
	}{
		{typ: "run_info", data: RunInfo{}},
		{typ: "index_selection", data: IndexSelection{}},
		{typ: "match", data: MatchedPair{}},
		{typ: "amount_diff", data: AmountDiffPair{}},
		{typ: "timing_diff", data: TimingDiffPair{}},
		{typ: "unmatched_left", data: Transaction{}},
		{typ: "unmatched_right", data: Transaction{}},
		{typ: "grouped_match", data: GroupedMatchedPair{}},
		{typ: "grouped_amount_diff", data: GroupedAmountDiffPair{}},
		{typ: "grouped_timing_diff", data: GroupedTimingDiffPair{}},
		{typ: "many_to_many_match", data: ManyToManyMatchedPair{}},
		{typ: "many_to_many_amount_diff", data: ManyToManyAmountDiffPair{}},
		{typ: "many_to_many_timing_diff", data: ManyToManyTimingDiffPair{}},
		{typ: "ambiguous_group", data: AmbiguousGroupPair{}},
		{typ: "duplicate", data: DuplicateGroup{}},
		{typ: "source_summary", data: SourceSummary{}},
		{typ: "summary", data: Summary{}},
	}
	for _, event := range events {
		t.Run(event.typ, func(t *testing.T) {
			validateJSON(t, resolved, map[string]any{
				"schema": ResultSchemaV1,
				"type":   event.typ,
				"data":   event.data,
			})
		})
	}
}

func validateJSON(t *testing.T, schema *jsonschema.Resolved, value any) {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal validation fixture: %v", err)
	}
	var instance any
	if err := json.Unmarshal(b, &instance); err != nil {
		t.Fatalf("unmarshal validation fixture: %v", err)
	}
	if err := schema.Validate(instance); err != nil {
		t.Fatalf("schema validation failed: %v\n%s", err, b)
	}
}
