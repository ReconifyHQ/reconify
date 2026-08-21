package schemas

import (
	_ "embed"

	"github.com/reconifyhq/reconify/engine/domain"
)

// ExplanationSchemaV1 is the stable identifier emitted by reconify explain.
const ExplanationSchemaV1 = "reconify.engine.explanation.v1"

// Explanation is a deterministic summary of a completed reconciliation result.
type Explanation struct {
	Schema          string                    `json:"schema"`
	Summary         domain.Summary            `json:"summary"`
	BySource        map[string]domain.Summary `json:"by_source,omitempty"`
	Findings        []Finding                 `json:"findings"`
	TopExceptions   []Exception               `json:"top_exceptions"`
	ExceptionsTotal int                       `json:"exceptions_total"`
	Truncated       bool                      `json:"truncated"`
}

// Finding is a deterministic count for one result event category.
type Finding struct {
	Category string `json:"category"`
	Count    int    `json:"count"`
}

// Exception is one bounded exception event copied from the source result.
type Exception struct {
	Type string                 `json:"type"`
	Data map[string]interface{} `json:"data"`
}

//go:generate go run ../cmd/generate-explanation-schema -output reconify.engine.explanation.v1.json

//go:embed reconify.engine.explanation.v1.json
var explanationSchemaV1 []byte

// ExplanationV1 returns a copy of the published explanation schema.
func ExplanationV1() []byte { return append([]byte(nil), explanationSchemaV1...) }
