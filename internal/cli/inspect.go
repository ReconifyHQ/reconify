package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/reconifyhq/reconify/config"
	"github.com/reconifyhq/reconify/engine/profile"
	"github.com/spf13/cobra"
)

func newInspectCmd() *cobra.Command {
	var format string
	var full bool
	var sampleValues int
	var sheet string

	cmd := &cobra.Command{
		Use:   "inspect FILE",
		Short: "Profile an input file deterministically before writing a config",
		Long: `Print a deterministic, machine-readable profile of an input file: format,
scan bounds, and per-column type inference (date layouts, amount formats,
representative sample values). Does not require or propose a reconify.yaml
column mapping; see "config infer" for that.

Scans up to 1,000 rows by default. Use --full for an exact scan.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			filePath := args[0]
			if _, err := os.Stat(filePath); err != nil {
				return inputErr(ErrCodeConfig, "config_error", fmt.Sprintf("file %q not found", filePath), diagnosticCodeInputUnreadable)
			}

			result, err := profile.Inspect(cmd.Context(), filePath, config.ParserCfg{Sheet: sheet}, profile.Options{
				Full:         full,
				SampleValues: sampleValues,
			})
			if err != nil {
				return inputErr(ErrCodeConfig, "config_error", fmt.Sprintf("inspect %q: %v", filePath, err), diagnosticCodeInputMismatch)
			}

			switch format {
			case "json":
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(result)
			default:
				return fmt.Errorf("unknown format %q (valid: json)", format)
			}
		},
	}

	cmd.Flags().StringVar(&format, "format", "json", "Output format: json (default)")
	cmd.Flags().BoolVar(&full, "full", false, "Perform an exact full-file scan instead of the default 1,000-row bounded scan")
	cmd.Flags().IntVar(&sampleValues, "sample-values", profile.DefaultSampleValues, "Representative raw sample values per column (0 disables)")
	cmd.Flags().StringVar(&sheet, "sheet", "", "XLSX/XLSM sheet name (defaults to the first sheet)")

	return cmd
}
