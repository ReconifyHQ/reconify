package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	configFile string
	verbose    bool
)

// Execute runs the CLI application
func Execute(version, buildTime string) error {
	rootCmd := &cobra.Command{
		Use:   "reconify",
		Short: "A developer-first reconciliation engine",
		Long: `Reconify is a reconciliation engine designed for finance, ops, and accounting teams.
It ingests financial data from multiple sources, normalizes them, and compares transactions.`,
		Version: fmt.Sprintf("%s (built %s)", version, buildTime),
	}

	// Global flags
	rootCmd.PersistentFlags().StringVarP(&configFile, "config", "c", "reconify.yaml", "Path to configuration file")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose output")

	// Add subcommands
	rootCmd.AddCommand(newConfigCmd())
	rootCmd.AddCommand(newReconcileCmd())
	rootCmd.AddCommand(newParseCmd())

	return rootCmd.Execute()
}

// getConfigPath returns the config file path, checking environment variable if not set
func getConfigPath() string {
	if configFile != "" {
		return configFile
	}
	if envConfig := os.Getenv("RECONIFY_CONFIG"); envConfig != "" {
		return envConfig
	}
	return "reconify.yaml"
}

