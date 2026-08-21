// Command generate-explanation-schema writes the published explanation schema.
package main

import (
	"flag"
	"fmt"
	"os"

	internalSchema "github.com/reconifyhq/reconify/internal/schema"
)

func main() {
	output := flag.String("output", "schemas/reconify.engine.explanation.v1.json", "schema output path")
	flag.Parse()
	document, err := internalSchema.GenerateExplanationSchemaJSON()
	if err != nil {
		fatal(err)
	}
	if err := os.WriteFile(*output, document, 0o644); err != nil { // #nosec G306 -- generated schema is a checked-in public artifact.
		fatal(fmt.Errorf("write %s: %w", *output, err))
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
