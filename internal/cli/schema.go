package cli

import (
	"fmt"

	"github.com/reconifyhq/reconify/schemas"
	"github.com/spf13/cobra"
)

func newSchemaCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schema",
		Short: "Print published Engine schemas",
	}
	cmd.AddCommand(newResultSchemaCmd())
	cmd.AddCommand(newDiagnosticSchemaCmd())
	return cmd
}

func newResultSchemaCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "result",
		Short: "Print the reconciliation result schema",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = args
			if _, err := cmd.OutOrStdout().Write(schemas.ResultV1()); err != nil {
				return fmt.Errorf("write result schema: %w", err)
			}
			return nil
		},
	}
}

func newDiagnosticSchemaCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "diagnostic",
		Short: "Print the structured diagnostic schema",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = args
			if _, err := cmd.OutOrStdout().Write(schemas.DiagnosticV1()); err != nil {
				return fmt.Errorf("write diagnostic schema: %w", err)
			}
			return nil
		},
	}
}
