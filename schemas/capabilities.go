// Package schemas exposes the published Reconify Engine schemas to the CLI
// and other local integrations.
package schemas

import _ "embed"

// CapabilitiesSchemaV1 is the stable identifier for the published Engine
// capabilities document.
const CapabilitiesSchemaV1 = "reconify.engine.capabilities.v1"

// Capabilities describes the implemented, agent-facing Engine surface.
type Capabilities struct {
	Schema           string                         `json:"schema"`
	ProtocolVersion  string                         `json:"protocol_version"`
	ProtocolVersions []string                       `json:"protocol_versions"`
	Engine           EngineCapabilities             `json:"engine"`
	Commands         map[string]CommandCapability   `json:"commands"`
	Formats          map[string]FormatCapability    `json:"formats"`
	Matching         MatchingCapabilities           `json:"matching"`
	ResultModes      []string                       `json:"result_modes"`
	Schemas          map[string]string              `json:"schemas"`
	ErrorCodes       map[string]ErrorCodeCapability `json:"error_codes"`
	ExitCodes        map[string]string              `json:"exit_codes"`
}

// EngineCapabilities identifies the executable and the protocol it serves.
type EngineCapabilities struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// CommandCapability describes one command available to an agent or script.
type CommandCapability struct {
	Description string `json:"description"`
	Interactive bool   `json:"interactive"`
}

// FormatCapability describes the supported input/output properties for a
// command family. Optional fields are omitted for formats where they do not
// apply.
type FormatCapability struct {
	Formats                 []string `json:"formats"`
	Default                 string   `json:"default"`
	StreamingFormats        []string `json:"streaming_formats,omitempty"`
	AuditCompatible         []string `json:"audit_compatible,omitempty"`
	DeterministicCompatible []string `json:"deterministic_compatible,omitempty"`
}

// MatchingCapabilities describes the configured matching pipeline options.
type MatchingCapabilities struct {
	Passes        []MatchingPass `json:"passes"`
	DefaultPasses []string       `json:"default_passes"`
	GroupKeys     []string       `json:"group_keys"`
}

// MatchingPass describes one supported reconciliation strategy.
type MatchingPass struct {
	Type        string   `json:"type"`
	Description string   `json:"description"`
	Cardinality string   `json:"cardinality"`
	Requires    []string `json:"requires,omitempty"`
	GroupBy     []string `json:"group_by,omitempty"`
}

// ErrorCodeCapability maps a granular diagnostic code to its legacy CLI
// compatibility code and process exit code.
type ErrorCodeCapability struct {
	Category    string `json:"category"`
	LegacyCode  string `json:"legacy_code"`
	ExitCode    int    `json:"exit_code"`
	Description string `json:"description"`
}

//go:generate go run ../cmd/generate-capabilities-schema -output reconify.engine.capabilities.v1.json

// capabilitiesSchemaV1 is the checked-in schema served by the CLI.
//
//go:embed reconify.engine.capabilities.v1.json
var capabilitiesSchemaV1 []byte

// CapabilitiesV1 returns a copy of the published capabilities schema document.
func CapabilitiesV1() []byte {
	return append([]byte(nil), capabilitiesSchemaV1...)
}
