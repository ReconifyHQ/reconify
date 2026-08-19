package schemas

import _ "embed"

// DiagnosticSchemaV1 is the stable identifier for structured Engine
// diagnostics emitted by the CLI.
const DiagnosticSchemaV1 = "reconify.engine.diagnostic.v1"

// Diagnostic contains the actionable, machine-readable explanation for a
// failed Engine command.
type Diagnostic struct {
	Code        string         `json:"code"`
	Category    string         `json:"category"`
	Message     string         `json:"message"`
	Details     map[string]any `json:"details"`
	Suggestions []string       `json:"suggestions"`
}

// DiagnosticEnvelope is the additive JSON error contract. Error and Code are
// retained for compatibility with the original --error-format json output.
type DiagnosticEnvelope struct {
	Error      string     `json:"error"`
	Code       string     `json:"code"`
	OK         bool       `json:"ok"`
	Schema     string     `json:"schema"`
	Diagnostic Diagnostic `json:"diagnostic"`
}

//go:generate go run ../cmd/generate-diagnostic-schema -output reconify.engine.diagnostic.v1.json

// diagnosticSchemaV1 is the checked-in schema served by the CLI.
//
//go:embed reconify.engine.diagnostic.v1.json
var diagnosticSchemaV1 []byte

// DiagnosticV1 returns a copy of the published diagnostic schema document.
func DiagnosticV1() []byte {
	return append([]byte(nil), diagnosticSchemaV1...)
}
