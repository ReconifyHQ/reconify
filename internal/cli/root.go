package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
)

var (
	configFile     string
	configExplicit bool
	verbose        bool
	errorFormat    string // "text" or "json"
	cliVersion     string // set by Execute; used by subcommands for audit envelopes
)

// ErrorFormat returns the current --error-format value. Read by main after Execute returns.
func ErrorFormat() string { return errorFormat }

// Execute runs the CLI application
func Execute(version, buildTime string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return newRootCmd(version, buildTime).ExecuteContext(ctx)
}

func newRootCmd(version, buildTime string) *cobra.Command {
	cliVersion = version
	rootCmd := &cobra.Command{
		Use:   "reconify",
		Short: "A developer-first reconciliation engine",
		Long: `Reconify is a reconciliation engine designed for finance, ops, and accounting teams.
It ingests financial data from multiple sources, normalizes them, and compares transactions.`,
		Version: fmt.Sprintf("%s (built %s)", version, buildTime),
		// Silence Cobra's built-in error and usage printing so main.go owns
		// the error output format (plain text or JSON via --error-format).
		SilenceErrors: true,
		SilenceUsage:  true,
	}

	// Global flags
	rootCmd.PersistentFlags().StringVarP(&configFile, "config", "c", "reconify.yaml", "Path to configuration file")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose output")
	rootCmd.PersistentFlags().StringVar(&errorFormat, "error-format", "text",
		`Error output format: text (default) or json. When json, errors are written to stderr as {"error":"...","code":"..."}.`)
	rootCmd.PersistentPreRun = func(cmd *cobra.Command, args []string) {
		_ = args
		configExplicit = cmd.Root().PersistentFlags().Changed("config")
	}

	// Add subcommands
	rootCmd.AddCommand(newConfigCmd())
	rootCmd.AddCommand(newReconcileCmd())
	rootCmd.AddCommand(newParseCmd())
	rootCmd.AddCommand(newSchemaCmd())

	return rootCmd
}

// getConfigPath returns the config file path, checking environment variable if not set
func getConfigPath() string {
	if configExplicit {
		return configFile
	}
	if envConfig := os.Getenv("RECONIFY_CONFIG"); envConfig != "" {
		return envConfig
	}
	return configFile
}
