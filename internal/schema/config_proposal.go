package schema

import (
	"encoding/json"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
	engineSchemas "github.com/reconifyhq/reconify/schemas"
)

const configProposalSchemaID = "urn:reconify:engine:config-proposal:v1"

// GenerateConfigProposalSchema constructs the published config proposal schema.
func GenerateConfigProposalSchema() (*jsonschema.Schema, error) {
	s, err := jsonschema.For[engineSchemas.ConfigProposal](nil)
	if err != nil {
		return nil, fmt.Errorf("reflect config proposal schema: %w", err)
	}
	s.ID, s.Schema = "", ""
	s.Defs = ensureDefs(s.Defs)
	def := s
	if s.Ref != "" {
		def = s.Defs["ConfigProposal"]
	}
	if def == nil || def.Properties["schema"] == nil {
		return nil, fmt.Errorf("reflected ConfigProposal schema has no schema property")
	}
	def.Properties["schema"].Const = jsonschema.Ptr[any](engineSchemas.ConfigProposalSchemaV1)
	return &jsonschema.Schema{ID: configProposalSchemaID, Schema: draft202012, Title: "Reconify Engine config proposal v1", Description: "Versioned config inference response emitted by reconify config infer.", OneOf: []*jsonschema.Schema{s.CloneSchemas()}, Defs: s.Defs}, nil
}

// GenerateConfigProposalSchemaJSON returns the deterministic schema artifact.
func GenerateConfigProposalSchemaJSON() ([]byte, error) {
	s, err := GenerateConfigProposalSchema()
	if err != nil {
		return nil, err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal config proposal schema: %w", err)
	}
	return append(b, '\n'), nil
}
