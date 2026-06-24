// Packagew cmd handles the main entry point for the reconify CLI application
package main

import (
	"encoding/json"
	"errors"
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
		exitCode := 1
		errCode := "error"

		var cliErr *cli.CLIError
		if errors.As(err, &cliErr) {
			exitCode = cliErr.Code
			errCode = cliErr.ErrCode
		}

		if cli.ErrorFormat() == "json" {
			out, _ := json.Marshal(map[string]string{"error": err.Error(), "code": errCode})
			fmt.Fprintf(os.Stderr, "%s\n", out)
		} else {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		os.Exit(exitCode)
	}
}
