// Packagew cmd handles the main entry point for the reconify CLI application
package main

import (
	"fmt"
	"os"

	"github.com/reconifyhq/reconify/internal/cli"
)

var (
	Version   = "dev"
	BuildTime = "unknown"
)

func main() {
	if err := cli.Execute(Version, BuildTime); err != nil {
		exitCode := cli.ExitCode(err)

		if cli.ErrorFormat() == "json" {
			out, marshalErr := cli.MarshalDiagnosticEnvelope(err)
			if marshalErr != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", marshalErr)
				os.Exit(1)
			}
			fmt.Fprintf(os.Stderr, "%s\n", out)
		} else {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		os.Exit(exitCode)
	}
}
