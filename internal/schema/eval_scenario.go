package schema

import (
	"encoding/json"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
	engineSchemas "github.com/reconifyhq/reconify/schemas"
)

const evalScenarioSchemaID = "urn:reconify:engine:eval-scenario:v1"
const evalScenarioSchemaV2ID = "urn:reconify:engine:eval-scenario:v2"

// GenerateEvalScenarioSchema constructs the published schema for one graded
// fixture in the Engine agent evaluation corpus under evals/.
func GenerateEvalScenarioSchema() (*jsonschema.Schema, error) {
	scenario, err := jsonschema.For[engineSchemas.EvalScenario](nil)
	if err != nil {
		return nil, fmt.Errorf("reflect eval scenario schema: %w", err)
	}

	scenario.ID = ""
	scenario.Schema = ""
	scenario.Defs = ensureDefs(scenario.Defs)
	scenarioDef := scenario
	if scenario.Ref != "" {
		scenarioDef = scenario.Defs["EvalScenario"]
		if scenarioDef == nil {
			return nil, fmt.Errorf("reflected eval scenario schema has no EvalScenario definition")
		}
	}
	if property := scenarioDef.Properties["schema"]; property != nil {
		property.Const = jsonschema.Ptr[any](engineSchemas.EvalScenarioSchemaV1)
	} else {
		return nil, fmt.Errorf("reflected EvalScenario schema has no schema property")
	}
	for _, name := range []string{"inputs", "counter_examples"} {
		if property := scenarioDef.Properties[name]; property != nil {
			property.Types = nil
			property.Type = "array"
		}
	}

	return &jsonschema.Schema{
		ID:          evalScenarioSchemaID,
		Schema:      draft202012,
		Title:       "Reconify Engine agent evaluation scenario v1",
		Description: "Versioned JSON contract for one graded fixture in the Engine agent evaluation corpus.",
		OneOf: []*jsonschema.Schema{
			scenario.CloneSchemas(),
		},
		Defs: scenario.Defs,
	}, nil
}

// GenerateEvalScenarioSchemaJSON returns the deterministic, pretty-printed
// schema document committed under schemas/.
func GenerateEvalScenarioSchemaJSON() ([]byte, error) {
	return generateEvalScenarioSchemaJSON(GenerateEvalScenarioSchema)
}

// GenerateEvalScenarioV2Schema constructs the full-workflow evaluation
// scenario contract. V2 adds a deterministic explanation answer key while V1
// remains available for existing third-party fixtures.
func GenerateEvalScenarioV2Schema() (*jsonschema.Schema, error) {
	scenario, err := jsonschema.For[engineSchemas.EvalScenarioV2](nil)
	if err != nil {
		return nil, fmt.Errorf("reflect eval scenario v2 schema: %w", err)
	}

	scenario.ID = ""
	scenario.Schema = ""
	scenario.Defs = ensureDefs(scenario.Defs)
	scenarioDef := scenario
	if scenario.Ref != "" {
		scenarioDef = scenario.Defs["EvalScenarioV2"]
		if scenarioDef == nil {
			return nil, fmt.Errorf("reflected EvalScenarioV2 schema has no definition")
		}
	}
	if property := scenarioDef.Properties["schema"]; property != nil {
		property.Const = jsonschema.Ptr[any](engineSchemas.EvalScenarioSchemaV2)
	} else {
		return nil, fmt.Errorf("reflected EvalScenarioV2 schema has no schema property")
	}
	for _, name := range []string{"inputs", "counter_examples"} {
		if property := scenarioDef.Properties[name]; property != nil {
			property.Types = nil
			property.Type = "array"
		}
	}

	return &jsonschema.Schema{
		ID:          evalScenarioSchemaV2ID,
		Schema:      draft202012,
		Title:       "Reconify Engine agent evaluation scenario v2",
		Description: "Versioned full-workflow fixture contract for the Engine agent evaluation corpus.",
		OneOf:       []*jsonschema.Schema{scenario.CloneSchemas()},
		Defs:        scenario.Defs,
	}, nil
}

// GenerateEvalScenarioV2SchemaJSON returns the deterministic v2 schema artifact.
func GenerateEvalScenarioV2SchemaJSON() ([]byte, error) {
	return generateEvalScenarioSchemaJSON(GenerateEvalScenarioV2Schema)
}

func generateEvalScenarioSchemaJSON(generate func() (*jsonschema.Schema, error)) ([]byte, error) {
	s, err := generate()
	if err != nil {
		return nil, err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal eval scenario schema: %w", err)
	}
	return append(b, '\n'), nil
}
