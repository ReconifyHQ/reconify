// generate-eval-explanations refreshes deterministic explanation answer keys.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/reconifyhq/reconify/engine/explain"
)

func main() {
	corpus := flag.String("corpus", "evals", "evaluation corpus directory")
	flag.Parse()
	entries, err := os.ReadDir(*corpus)
	if err != nil {
		fail(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		resultPath := filepath.Join(*corpus, entry.Name(), "expected", "result.json")
		input, err := os.Open(resultPath) // #nosec G304 -- checked-in corpus path.
		if err != nil {
			fail(err)
		}
		result, err := explain.Explain(input, explain.Options{TopN: 10})
		closeErr := input.Close()
		if err != nil {
			fail(err)
		}
		if closeErr != nil {
			fail(closeErr)
		}
		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			fail(err)
		}
		data = append(data, '\n')
		output := filepath.Join(*corpus, entry.Name(), "expected", "explanation.json")
		if err := os.WriteFile(output, data, 0o600); err != nil {
			fail(err)
		} // #nosec G703 -- checked-in corpus path.
	}
}

func fail(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
