package schema

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/reconifyhq/reconify/schemas"
)

func TestPublishedCapabilitiesSchemaHasNoDrift(t *testing.T) {
	generated, err := GenerateCapabilitiesSchemaJSON()
	if err != nil {
		t.Fatal(err)
	}
	if got := schemas.CapabilitiesV1(); !reflect.DeepEqual(generated, got) {
		t.Fatalf("published schema drifted from generator (generated %d bytes, published %d bytes)", len(generated), len(got))
	}
}

func TestPublishedCapabilitiesSchemaValidatesDocument(t *testing.T) {
	var document jsonschema.Schema
	if err := json.Unmarshal(schemas.CapabilitiesV1(), &document); err != nil {
		t.Fatalf("unmarshal published schema: %v", err)
	}
	resolved, err := document.Resolve(nil)
	if err != nil {
		t.Fatalf("resolve published schema: %v", err)
	}

	validateJSON(t, resolved, schemas.Capabilities{
		Schema:           schemas.CapabilitiesSchemaV1,
		ProtocolVersion:  "v1",
		ProtocolVersions: []string{"v1"},
		Engine:           schemas.EngineCapabilities{Name: "Reconify Engine", Version: "test"},
		Commands: map[string]schemas.CommandCapability{
			"capabilities": {Description: "describe", Interactive: false},
		},
		Formats: map[string]schemas.FormatCapability{
			"parse": {Formats: []string{"ndjson"}, Default: "ndjson"},
		},
		Matching: schemas.MatchingCapabilities{
			Passes:        []schemas.MatchingPass{{Type: "reference_one_to_one", Description: "match", Cardinality: "one_to_one"}},
			DefaultPasses: []string{"reference_one_to_one"},
			GroupKeys:     []string{"reference"},
		},
		ResultModes: []string{"all"},
		Schemas:     map[string]string{"result": schemas.ResultSchemaV1},
		ErrorCodes: map[string]schemas.ErrorCodeCapability{
			"INTERNAL_ERROR": {Category: "internal", LegacyCode: "error", ExitCode: 1, Description: "internal"},
		},
		ExitCodes: map[string]string{"0": "Success."},
	})
}
