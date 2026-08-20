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
	cmd.AddCommand(newCapabilitiesSchemaCmd())
	cmd.AddCommand(newProfileSchemaCmd())
	cmd.AddCommand(newConfigProposalSchemaCmd())
	cmd.AddCommand(newExplanationSchemaCmd())
	return cmd
}

func newConfigProposalSchemaCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "config-proposal",
		Short: "Print the Engine config proposal schema",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = args
			if _, err := cmd.OutOrStdout().Write(schemas.ConfigProposalV1()); err != nil {
				return fmt.Errorf("write config proposal schema: %w", err)
			}
			return nil
		},
	}
}

func newExplanationSchemaCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "explanation",
		Short: "Print the Engine explanation schema",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = args
			if _, err := cmd.OutOrStdout().Write(schemas.ExplanationV1()); err != nil {
				return fmt.Errorf("write explanation schema: %w", err)
			}
			return nil
		},
	}
}

func newProfileSchemaCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "profile",
		Short: "Print the Engine file profile schema",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = args
			if _, err := cmd.OutOrStdout().Write(schemas.ProfileV1()); err != nil {
				return fmt.Errorf("write profile schema: %w", err)
			}
			return nil
		},
	}
}

func newCapabilitiesSchemaCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "capabilities",
		Short: "Print the Engine capabilities schema",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = args
			if _, err := cmd.OutOrStdout().Write(schemas.CapabilitiesV1()); err != nil {
				return fmt.Errorf("write capabilities schema: %w", err)
			}
			return nil
		},
	}
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
