package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/reconifyhq/reconify/engine/explain"
	"github.com/spf13/cobra"
)

func newExplainCmd() *cobra.Command {
	var top int
	cmd := &cobra.Command{
		Use:   "explain FILE",
		Short: "Summarize a completed reconciliation result",
		Long: `Read a JSON, JSON-stream, or NDJSON reconciliation result and emit a
deterministic reconify.engine.explanation.v1 summary. No reconciliation is run
and no subjective severity or prose conclusions are added.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if top < 0 {
				return configErr("--top must be non-negative")
			}
			filePath := args[0]
			if _, err := os.Stat(filePath); err != nil {
				return inputErr(ErrCodeConfig, "config_error", fmt.Sprintf("file %q not found", filePath), diagnosticCodeInputUnreadable)
			}
			file, err := os.Open(filePath) // #nosec G304 -- path is explicit CLI input.
			if err != nil {
				return inputErr(ErrCodeConfig, "config_error", fmt.Sprintf("open result %q: %v", filePath, err), diagnosticCodeInputUnreadable)
			}
			defer func() { _ = file.Close() }()
			result, err := explain.Explain(file, explain.Options{TopN: top})
			if err != nil {
				return inputErr(ErrCodeConfig, "config_error", fmt.Sprintf("explain %q: %v", filePath, err), diagnosticCodeInputMismatch)
			}
			encoder := json.NewEncoder(cmd.OutOrStdout())
			encoder.SetIndent("", "  ")
			return encoder.Encode(result)
		},
	}
	cmd.Flags().IntVar(&top, "top", 10, "Maximum exception events to include")
	return cmd
}
