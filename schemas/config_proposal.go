package schemas

import _ "embed"

// ConfigProposalSchemaV1 is the stable identifier emitted by config infer.
const ConfigProposalSchemaV1 = "reconify.engine.config-proposal.v1"

// ConfigProposal is a deterministic, non-interactive reconify.yaml proposal.
type ConfigProposal struct {
	Schema       string           `json:"schema"`
	Status       string           `json:"status"`
	Sources      []InferredSource `json:"sources"`
	ProposedYAML string           `json:"proposed_yaml"`
	Reasons      []string         `json:"reasons,omitempty"`
}

// InferredSource describes one source file and its proposed mappings.
type InferredSource struct {
	Name       string              `json:"name"`
	File       string              `json:"file"`
	Format     string              `json:"format"`
	Validation InferenceValidation `json:"validation"`
	Mappings   []InferredRole      `json:"mappings"`
}

// InferenceValidation summarizes the bounded parsing validation scan.
type InferenceValidation struct {
	RowsScanned    int  `json:"rows_scanned"`
	SuccessfulRows int  `json:"successful_rows"`
	Truncated      bool `json:"truncated"`
}

// InferredRole describes a selected column and ranked alternatives for a role.
type InferredRole struct {
	Role         string             `json:"role"`
	Column       string             `json:"column"`
	Confidence   float64            `json:"confidence"`
	Lead         float64            `json:"lead"`
	Ready        bool               `json:"ready"`
	Alternatives []MappingCandidate `json:"alternatives"`
	Reasons      []string           `json:"reasons,omitempty"`
}

// MappingCandidate is one column candidate for an inferred role.
type MappingCandidate struct {
	Column     string  `json:"column"`
	Confidence float64 `json:"confidence"`
}

//go:generate go run ../cmd/generate-config-proposal-schema -output reconify.engine.config-proposal.v1.json

//go:embed reconify.engine.config-proposal.v1.json
var configProposalSchemaV1 []byte

// ConfigProposalV1 returns a copy of the published config proposal schema.
func ConfigProposalV1() []byte { return append([]byte(nil), configProposalSchemaV1...) }
