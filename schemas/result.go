// Package schemas exposes the published Reconify Engine schemas to the CLI
// and other local integrations.
package schemas

import (
	_ "embed"

	"github.com/reconifyhq/reconify/engine/domain"
)

// ResultSchemaV1 is the stable identifier for the published result schema.
const ResultSchemaV1 = domain.ResultSchemaV1

//go:generate go run ../cmd/generate-result-schema -output reconify.engine.result.v1.json

// resultSchemaV1 is the checked-in schema served by the CLI.
//
//go:embed reconify.engine.result.v1.json
var resultSchemaV1 []byte

// ResultV1 returns a copy of the published result schema document.
func ResultV1() []byte {
	return append([]byte(nil), resultSchemaV1...)
}
