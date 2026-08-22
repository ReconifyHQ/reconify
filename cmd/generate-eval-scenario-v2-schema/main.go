// generate-eval-scenario-v2-schema writes the published v2 corpus contract.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/reconifyhq/reconify/internal/schema"
)

func main() {
	output := flag.String("output", "schemas/reconify.engine.eval-scenario.v2.json", "schema output path")
	flag.Parse()

	data, err := schema.GenerateEvalScenarioV2SchemaJSON()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(*output, data, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
