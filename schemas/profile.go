package schemas

import _ "embed"

// ProfileSchemaV1 is the stable identifier for the published Engine file
// profile document emitted by `reconify inspect`.
const ProfileSchemaV1 = "reconify.engine.profile.v1"

// Profile is the deterministic, agent-facing description of an input file's
// structure: format, scan bounds, and per-column type inference. It does not
// propose a column mapping or config; see `config infer` (AP-6) for that.
type Profile struct {
	Schema  string          `json:"schema"`
	File    string          `json:"file"`
	Format  string          `json:"format"`
	Scan    ScanSummary     `json:"scan"`
	Columns []ColumnProfile `json:"columns"`
}

// ScanSummary describes how much of the file was read to build the profile.
type ScanSummary struct {
	Full        bool `json:"full"`
	RowsScanned int  `json:"rows_scanned"`
	Truncated   bool `json:"truncated"`
}

// ColumnProfile describes one input column's observed shape across the
// scanned rows.
type ColumnProfile struct {
	Name          string          `json:"name"`
	NonEmptyCount int             `json:"non_empty_count"`
	NullCount     int             `json:"null_count"`
	InferredType  string          `json:"inferred_type"`
	Ambiguous     bool            `json:"ambiguous"`
	Candidates    []TypeCandidate `json:"candidates"`
	DateLayout    string          `json:"date_layout,omitempty"`
	AmountFormat  *AmountFormat   `json:"amount_format,omitempty"`
	SampleValues  []string        `json:"sample_values,omitempty"`
}

// TypeCandidate is one type hypothesis considered for a column, ranked by
// confidence (fraction of non-empty sampled values that matched).
type TypeCandidate struct {
	Type       string  `json:"type"`
	Confidence float64 `json:"confidence"`
}

// AmountFormat describes the numeric convention detected for an amount-typed
// column.
type AmountFormat struct {
	Decimal               string `json:"decimal"`
	Thousands             string `json:"thousands,omitempty"`
	CurrencySymbol        string `json:"currency_symbol,omitempty"`
	ParenthesizedNegative bool   `json:"parenthesized_negative"`
}

//go:generate go run ../cmd/generate-profile-schema -output reconify.engine.profile.v1.json

// profileSchemaV1 is the checked-in schema served by the CLI.
//
//go:embed reconify.engine.profile.v1.json
var profileSchemaV1 []byte

// ProfileV1 returns a copy of the published profile schema document.
func ProfileV1() []byte {
	return append([]byte(nil), profileSchemaV1...)
}
