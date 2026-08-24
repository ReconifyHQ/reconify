// reconify-eval runs opt-in local coding-agent evaluations.
package main

import (
	"context"
	"encoding/json"
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
	if len(os.Args) < 2 || (os.Args[1] != "run" && os.Args[1] != "release") {
		fmt.Fprintln(os.Stderr, "usage: reconify-eval {run|release} ...")
		os.Exit(2)
	}
	if os.Args[1] == "release" {
		releaseMain(os.Args[2:])
		return
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

func releaseMain(args []string) {
	flags := flag.NewFlagSet("release", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	baseline := flags.String("baseline-version", "", "explicit published @reconifyhq/skills version")
	reconify := flags.String("reconify", "", "path to the Reconify binary (default: build current CLI)")
	corpus := flags.String("corpus", "evals", "evaluation corpus directory")
	out := flags.String("out-dir", ".context/evals", "directory for report and retained artifacts")
	seed := flags.Int64("seed", 0, "randomization seed")
	parallel := flags.Int("max-parallel", 1, "maximum active agent calls")
	resume := flags.Bool("resume", false, "reuse completed variant results in out-dir")
	timeout := flags.Duration("timeout", 10*time.Minute, "maximum duration of one trial")
	var models, scenarios stringsFlag
	flags.Var(&models, "model", "explicit model selection agent=id; repeat for all four agents")
	flags.Var(&scenarios, "scenario", "scenario ID to run")
	if err := flags.Parse(args); err != nil {
		os.Exit(2)
	}
	path := *reconify
	if path != "" {
		var err error
		path, err = absoluteReconifyPath(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	}
	report, err := evals.Release(context.Background(), evals.ReleaseOptions{CorpusDir: *corpus, ReconifyPath: path, BaselineVersion: *baseline, OutDir: *out, Models: models, Seed: *seed, MaxParallel: *parallel, Resume: *resume, Timeout: *timeout, ScenarioIDs: scenarios})
	if err != nil {
		fmt.Fprintln(os.Stderr, "reconify-eval release:", err)
		os.Exit(2)
	}
	data, _ := json.MarshalIndent(report, "", "  ")
	fmt.Println(string(data))
	if report.Verdict != nil {
		switch report.Verdict.Status {
		case "fail":
			os.Exit(3)
		case "inconclusive":
			os.Exit(2)
		}
	}
}

func absoluteReconifyPath(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("--reconify is required")
	}
	return filepath.Abs(value)
}
