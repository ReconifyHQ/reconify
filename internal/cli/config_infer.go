package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/reconifyhq/reconify/engine/inference"
	"github.com/spf13/cobra"
)

func newConfigInferCmd() *cobra.Command {
	var left, right, out string
	cmd := &cobra.Command{
		Use:   "infer --left FILE --right FILE [--out FILE]",
		Short: "Infer a deterministic reconify.yaml proposal from two input files",
		Long: `Infer date, amount, and reference mappings from two input files without prompts.
The command prints reconify.engine.config-proposal.v1 JSON. It returns needs_input
instead of guessing whenever confidence or sample-row gates are not satisfied.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = args
			if left == "" {
				return configErr("--left is required")
			}
			if right == "" {
				return configErr("--right is required")
			}
			proposal, err := inference.Infer(cmd.Context(), left, right)
			if err != nil {
				return inputErr(ErrCodeConfig, "config_error", fmt.Sprintf("infer config: %v", err), diagnosticCodeInputMismatch)
			}
			if err := json.NewEncoder(cmd.OutOrStdout()).Encode(proposal); err != nil {
				return fmt.Errorf("write proposal: %w", err)
			}
			if out == "" {
				return nil
			}
			if proposal.Status != "ready" {
				return inferenceAmbiguousErr(proposal.Reasons)
			}
			if err := writeInferredConfig(out, []byte(proposal.ProposedYAML)); err != nil {
				return configErrf("write inferred config: %v", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&left, "left", "", "Left input file")
	cmd.Flags().StringVar(&right, "right", "", "Right input file")
	cmd.Flags().StringVar(&out, "out", "", "Write the ready proposed YAML config to this path")
	return cmd
}

func inferenceAmbiguousErr(reasons []string) *Error {
	return newCLIError(ErrCodeConfig, "config_error", "inference needs input", diagnosticCodeInferenceAmbiguous, diagnosticCategoryInference,
		"Review the proposed alternatives, select mappings explicitly, and rerun `reconify config validate`.", map[string]any{"reasons": reasons})
}

func writeInferredConfig(path string, data []byte) error {
	if info, err := os.Stat(path); err == nil {
		if info.IsDir() {
			return fmt.Errorf("output path %q is a directory", path)
		}
		return fmt.Errorf("output path %q already exists", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("check output path %q: %w", path, err)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".reconify-infer-*")
	if err != nil {
		return fmt.Errorf("create temporary output: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temporary output: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("set output permissions: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary output: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("publish output: %w", err)
	}
	return nil
}
