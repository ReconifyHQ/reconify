package schema

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/reconifyhq/reconify/schemas"
)

func TestPublishedEvalScenarioSchemaHasNoDrift(t *testing.T) {
	generated, err := GenerateEvalScenarioSchemaJSON()
	if err != nil {
		t.Fatal(err)
	}
	if got := schemas.EvalScenarioV1(); !reflect.DeepEqual(generated, got) {
		t.Fatalf("published eval scenario schema drifted from generator (generated %d bytes, published %d bytes)", len(generated), len(got))
	}
}

func TestPublishedEvalScenarioSchemaValidatesDocument(t *testing.T) {
	var document jsonschema.Schema
	if err := json.Unmarshal(schemas.EvalScenarioV1(), &document); err != nil {
		t.Fatalf("unmarshal published eval scenario schema: %v", err)
	}
	resolved, err := document.Resolve(nil)
	if err != nil {
		t.Fatalf("resolve published eval scenario schema: %v", err)
	}

	validateJSON(t, resolved, schemas.EvalScenario{
		Schema:          schemas.EvalScenarioSchemaV1,
		ID:              "003-settlement-fee",
		Prompt:          "Our processor settles each sale net of its fee...",
		Inputs:          []string{"inputs/left.csv", "inputs/right.csv"},
		ReferenceConfig: "reference/reconify.yaml",
		ExpectedResult:  "expected/result.json",
		Pair:            "left_vs_right",
		Assertions: schemas.EvalAssertions{
			Matched:         1,
			AmountDiffCount: 1,
		},
		CounterExamples: []string{"counter_examples/tolerance-too-wide.yaml"},
	})
}
