package schema

import (
	"encoding/json"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
	engineSchemas "github.com/reconifyhq/reconify/schemas"
)

const explanationSchemaID = "urn:reconify:engine:explanation:v1"

// GenerateExplanationSchema constructs the published explanation schema.
func GenerateExplanationSchema() (*jsonschema.Schema, error) {
	explanation, err := jsonschema.For[engineSchemas.Explanation](nil)
	if err != nil {
		return nil, fmt.Errorf("reflect explanation schema: %w", err)
	}
	explanation.ID, explanation.Schema = "", ""
	explanation.Defs = ensureDefs(explanation.Defs)
	definition := explanation
	if explanation.Ref != "" {
		definition = explanation.Defs["Explanation"]
	}
	if definition == nil || definition.Properties["schema"] == nil {
		return nil, fmt.Errorf("reflected Explanation schema has no schema property")
	}
	definition.Properties["schema"].Const = jsonschema.Ptr[any](engineSchemas.ExplanationSchemaV1)
	return &jsonschema.Schema{
		ID:          explanationSchemaID,
		Schema:      draft202012,
		Title:       "Reconify Engine explanation v1",
		Description: "Versioned deterministic summary of a completed reconciliation result.",
		OneOf:       []*jsonschema.Schema{explanation.CloneSchemas()},
		Defs:        explanation.Defs,
	}, nil
}

// GenerateExplanationSchemaJSON returns the deterministic schema artifact.
func GenerateExplanationSchemaJSON() ([]byte, error) {
	schema, err := GenerateExplanationSchema()
	if err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal explanation schema: %w", err)
	}
	return append(data, '\n'), nil
}
