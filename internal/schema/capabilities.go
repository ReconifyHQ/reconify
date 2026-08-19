package schema

import (
	"encoding/json"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
	engineSchemas "github.com/reconifyhq/reconify/schemas"
)

const capabilitiesSchemaID = "urn:reconify:engine:capabilities:v1"

// GenerateCapabilitiesSchema constructs the published schema for the
// agent-facing Engine capabilities document.
func GenerateCapabilitiesSchema() (*jsonschema.Schema, error) {
	capabilities, err := jsonschema.For[engineSchemas.Capabilities](nil)
	if err != nil {
		return nil, fmt.Errorf("reflect capabilities schema: %w", err)
	}

	capabilities.ID = ""
	capabilities.Schema = ""
	capabilities.Defs = ensureDefs(capabilities.Defs)
	capabilitiesDef := capabilities
	if capabilities.Ref != "" {
		capabilitiesDef = capabilities.Defs["Capabilities"]
		if capabilitiesDef == nil {
			return nil, fmt.Errorf("reflected capabilities schema has no Capabilities definition")
		}
	}
	if property := capabilitiesDef.Properties["schema"]; property != nil {
		property.Const = jsonschema.Ptr[any](engineSchemas.CapabilitiesSchemaV1)
	} else {
		return nil, fmt.Errorf("reflected Capabilities schema has no schema property")
	}
	for _, name := range []string{"protocol_versions", "result_modes"} {
		if property := capabilitiesDef.Properties[name]; property != nil {
			property.Types = nil
			property.Type = "array"
		}
	}
	if matching := capabilitiesDef.Properties["matching"]; matching != nil {
		matchingDef := matching
		if matching.Ref != "" {
			name := matching.Ref
			const prefix = "#/$defs/"
			if len(name) > len(prefix) && name[:len(prefix)] == prefix {
				name = name[len(prefix):]
			}
			matchingDef = capabilities.Defs[name]
		}
		if matchingDef != nil {
			for _, name := range []string{"passes", "default_passes", "group_keys"} {
				if property := matchingDef.Properties[name]; property != nil {
					property.Types = nil
					property.Type = "array"
				}
			}
		}
	}

	return &jsonschema.Schema{
		ID:          capabilitiesSchemaID,
		Schema:      draft202012,
		Title:       "Reconify Engine capabilities v1",
		Description: "Versioned JSON description of the implemented Reconify Engine interface.",
		OneOf: []*jsonschema.Schema{
			capabilities.CloneSchemas(),
		},
		Defs: capabilities.Defs,
	}, nil
}

// GenerateCapabilitiesSchemaJSON returns the deterministic, pretty-printed
// schema document committed under schemas/.
func GenerateCapabilitiesSchemaJSON() ([]byte, error) {
	s, err := GenerateCapabilitiesSchema()
	if err != nil {
		return nil, err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal capabilities schema: %w", err)
	}
	return append(b, '\n'), nil
}
