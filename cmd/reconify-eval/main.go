// reconify-eval runs opt-in local coding-agent evaluations.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/reconifyhq/reconify/internal/evals"
)

type stringsFlag []string

func (values *stringsFlag) String() string         { return strings.Join(*values, ",") }
func (values *stringsFlag) Set(value string) error { *values = append(*values, value); return nil }

func main() {
	if len(os.Args) < 2 || os.Args[1] != "run" {
		fmt.Fprintln(os.Stderr, "usage: reconify-eval run --reconify PATH --agent <all|claude|codex|gemini|opencode> --trials N [--scenario ID] [--timeout D] [--out FILE]")
		os.Exit(2)
	}
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	reconify := flags.String("reconify", "", "path to the Reconify binary under evaluation")
	trials := flags.Int("trials", 0, "number of trials per agent and scenario (required)")
	timeout := flags.Duration("timeout", 10*time.Minute, "maximum duration of one agent trial")
	output := flags.String("out", "", "write the JSON report to this path instead of stdout")
	corpus := flags.String("corpus", "evals", "evaluation corpus directory")
	skills := flags.String("skills", "skills", "packaged skills directory")
	var agents, scenarios stringsFlag
	flags.Var(&agents, "agent", "agent to evaluate; repeat or use all (required)")
	flags.Var(&scenarios, "scenario", "scenario ID to run; repeat for multiple")
	if err := flags.Parse(os.Args[2:]); err != nil {
		os.Exit(2)
	}
	path, err := absoluteReconifyPath(*reconify)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	report, err := evals.Run(context.Background(), evals.Options{CorpusDir: *corpus, SkillsDir: *skills, ReconifyPath: path, Agents: agents, Trials: *trials, ScenarioIDs: scenarios, Timeout: *timeout})
	if err != nil {
		fmt.Fprintln(os.Stderr, "reconify-eval:", err)
		os.Exit(2)
	}
	if err := evals.WriteReport(report, *output, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "reconify-eval:", err)
		os.Exit(1)
	}
}

func absoluteReconifyPath(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("--reconify is required")
	}
	return filepath.Abs(value)
}
