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
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
