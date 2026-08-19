package cli

import (
	"encoding/json"

	"github.com/spf13/cobra"
)

func newCapabilitiesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "capabilities",
		Short: "Describe the installed Reconify Engine interface",
		Long: `Print the versioned, machine-readable capabilities document for the
installed Reconify Engine. Agents can use this to discover commands, formats,
matching passes, result modes, schemas, diagnostic codes, and exit codes.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = args
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(buildCapabilities())
		},
	}
}
