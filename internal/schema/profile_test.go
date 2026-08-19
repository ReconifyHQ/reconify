package schema

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/reconifyhq/reconify/schemas"
)

func TestPublishedProfileSchemaHasNoDrift(t *testing.T) {
	generated, err := GenerateProfileSchemaJSON()
	if err != nil {
		t.Fatal(err)
	}
	if got := schemas.ProfileV1(); !reflect.DeepEqual(generated, got) {
		t.Fatalf("published profile schema drifted from generator (generated %d bytes, published %d bytes)", len(generated), len(got))
	}
}

func TestPublishedProfileSchemaValidatesDocument(t *testing.T) {
	var document jsonschema.Schema
	if err := json.Unmarshal(schemas.ProfileV1(), &document); err != nil {
		t.Fatalf("unmarshal published profile schema: %v", err)
	}
	resolved, err := document.Resolve(nil)
	if err != nil {
		t.Fatalf("resolve published profile schema: %v", err)
	}

	validateJSON(t, resolved, schemas.Profile{
		Schema: schemas.ProfileSchemaV1,
		File:   "payments.csv",
		Format: "csv",
		Scan:   schemas.ScanSummary{Full: false, RowsScanned: 1000, Truncated: true},
		Columns: []schemas.ColumnProfile{
			{
				Name:          "date",
				NonEmptyCount: 1000,
				NullCount:     0,
				InferredType:  "date",
				Ambiguous:     false,
				Candidates: []schemas.TypeCandidate{
					{Type: "date", Confidence: 0.99},
					{Type: "text", Confidence: 0.3},
				},
				DateLayout:   "2006-01-02",
				SampleValues: []string{"2024-01-01", "2024-01-02"},
			},
			{
				Name:          "amount",
				NonEmptyCount: 1000,
				NullCount:     0,
				InferredType:  "amount",
				Ambiguous:     false,
				Candidates: []schemas.TypeCandidate{
					{Type: "amount", Confidence: 1},
				},
				AmountFormat: &schemas.AmountFormat{
					Decimal:               ".",
					Thousands:             ",",
					ParenthesizedNegative: true,
				},
			},
		},
	})
}
