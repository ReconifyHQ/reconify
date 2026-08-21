package schema

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/reconifyhq/reconify/schemas"
)

func TestPublishedConfigProposalSchemaHasNoDrift(t *testing.T) {
	generated, err := GenerateConfigProposalSchemaJSON()
	if err != nil {
		t.Fatal(err)
	}
	if got := schemas.ConfigProposalV1(); !reflect.DeepEqual(generated, got) {
		t.Fatal("published config proposal schema drifted")
	}
}

func TestPublishedConfigProposalSchemaValidatesProposal(t *testing.T) {
	var document jsonschema.Schema
	if err := json.Unmarshal(schemas.ConfigProposalV1(), &document); err != nil {
		t.Fatal(err)
	}
	resolved, err := document.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	proposal := schemas.ConfigProposal{
		Schema:       schemas.ConfigProposalSchemaV1,
		Status:       "needs_input",
		ProposedYAML: "version: 1\n",
		Sources: []schemas.InferredSource{{
			Name:       "left",
			File:       "/tmp/a.csv",
			Format:     "csv",
			Validation: schemas.InferenceValidation{RowsScanned: 10},
			Mappings: []schemas.InferredRole{{
				Role:         "date",
				Column:       "date",
				Alternatives: []schemas.MappingCandidate{{Column: "date", Confidence: 1}},
			}},
		}},
	}
	validateJSON(t, resolved, proposal)
}
