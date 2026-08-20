package schema

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/reconifyhq/reconify/engine/domain"
	"github.com/reconifyhq/reconify/schemas"
)

func TestPublishedExplanationSchemaHasNoDrift(t *testing.T) {
	generated, err := GenerateExplanationSchemaJSON()
	if err != nil {
		t.Fatal(err)
	}
	if got := schemas.ExplanationV1(); !reflect.DeepEqual(generated, got) {
		t.Fatal("published explanation schema drifted")
	}
}

func TestPublishedExplanationSchemaValidatesDocument(t *testing.T) {
	var document jsonschema.Schema
	if err := json.Unmarshal(schemas.ExplanationV1(), &document); err != nil {
		t.Fatal(err)
	}
	resolved, err := document.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	validateJSON(t, resolved, schemas.Explanation{
		Schema:        schemas.ExplanationSchemaV1,
		Summary:       domain.Summary{MatchedCount: 1},
		Findings:      []schemas.Finding{{Category: "matched", Count: 1}},
		TopExceptions: []schemas.Exception{{Type: "amount_diff", Data: map[string]interface{}{"diff_minor": float64(2)}}},
	})
}
