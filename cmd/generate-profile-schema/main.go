// Package main writes the checked-in Reconify Engine profile schema.
package main

import (
	"flag"
	"fmt"
	"os"

	internalSchema "github.com/reconifyhq/reconify/internal/schema"
)

func main() {
	output := flag.String("output", "schemas/reconify.engine.profile.v1.json", "schema output path")
	flag.Parse()

	document, err := internalSchema.GenerateProfileSchemaJSON()
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
