package schema

import (
	"encoding/json"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
	engineSchemas "github.com/reconifyhq/reconify/schemas"
)

const profileSchemaID = "urn:reconify:engine:profile:v1"

// GenerateProfileSchema constructs the published schema for the agent-facing
// Engine file profile document emitted by `reconify inspect`.
func GenerateProfileSchema() (*jsonschema.Schema, error) {
	profile, err := jsonschema.For[engineSchemas.Profile](nil)
	if err != nil {
		return nil, fmt.Errorf("reflect profile schema: %w", err)
	}

	profile.ID = ""
	profile.Schema = ""
	profile.Defs = ensureDefs(profile.Defs)
	profileDef := profile
	if profile.Ref != "" {
		profileDef = profile.Defs["Profile"]
		if profileDef == nil {
			return nil, fmt.Errorf("reflected profile schema has no Profile definition")
		}
	}
	if property := profileDef.Properties["schema"]; property != nil {
		property.Const = jsonschema.Ptr[any](engineSchemas.ProfileSchemaV1)
	} else {
		return nil, fmt.Errorf("reflected Profile schema has no schema property")
	}
	if property := profileDef.Properties["columns"]; property != nil {
		property.Types = nil
		property.Type = "array"
		columnItems := property.Items
		if columnItems != nil {
			if candidates := columnItems.Properties["candidates"]; candidates != nil {
				candidates.Types = nil
				candidates.Type = "array"
			}
		}
	}

	return &jsonschema.Schema{
		ID:          profileSchemaID,
		Schema:      draft202012,
		Title:       "Reconify Engine file profile v1",
		Description: "Versioned JSON description of a deterministically profiled input file, emitted by `reconify inspect`.",
		OneOf: []*jsonschema.Schema{
			profile.CloneSchemas(),
		},
		Defs: profile.Defs,
	}, nil
}

// GenerateProfileSchemaJSON returns the deterministic, pretty-printed schema
// document committed under schemas/.
func GenerateProfileSchemaJSON() ([]byte, error) {
	s, err := GenerateProfileSchema()
	if err != nil {
		return nil, err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal profile schema: %w", err)
	}
	return append(b, '\n'), nil
}
