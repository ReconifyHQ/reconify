// Package evals runs the checked-in Engine agent corpus against local coding
// agent CLIs. It only starts external agents when reconify-eval is explicitly run.
package evals

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/reconifyhq/reconify/schemas"
)

const reportSchemaV1 = "reconify.engine.eval-report.v1"

// Options configures one explicit evaluator run.
type Options struct {
	CorpusDir, SkillsDir, ReconifyPath string
	Agents, ScenarioIDs                []string
	Trials                             int
	Timeout                            time.Duration
	LookPath                           func(string) (string, error)
}

// Report is the aggregate machine-readable evaluator output.
type Report struct {
	Schema  string         `json:"schema"`
	Agents  []AgentReport  `json:"agents"`
	Skipped []SkippedAgent `json:"skipped,omitempty"`
}

// SkippedAgent records an unavailable agent from an `--agent all` run.
type SkippedAgent struct {
	Agent  string `json:"agent"`
	Reason string `json:"reason"`
}

// AgentReport groups results from one adapter.
type AgentReport struct {
	Agent     string           `json:"agent"`
	Scenarios []ScenarioReport `json:"scenarios"`
}

// ScenarioReport contains every trial for one corpus scenario.
type ScenarioReport struct {
	ID             string        `json:"id"`
	Trials         []TrialReport `json:"trials"`
	Classification Metrics       `json:"classification"`
	ExactResult    Metrics       `json:"exact_result"`
}

// Metrics reports capability and reliability across a scenario's trials.
type Metrics struct {
	PassAt1 bool `json:"pass_at_1"`
	PassAll bool `json:"pass_all"`
}

// TrialReport records observable workflow evidence for one agent attempt.
type TrialReport struct {
	Trial          int      `json:"trial"`
	Discovery      bool     `json:"discovery"`
	Configuration  bool     `json:"configuration"`
	Execution      bool     `json:"execution"`
	Classification bool     `json:"classification"`
	Explanation    bool     `json:"explanation"`
	ExactResult    bool     `json:"exact_result"`
	Commands       []string `json:"commands"`
	AgentOutput    string   `json:"agent_output,omitempty"`
	Error          string   `json:"error,omitempty"`
}

type scenario struct {
	ID, Prompt, ExpectedResult, ExpectedExplanation, Pair, Dir string
	Inputs                                                     []string
	Assertions                                                 schemas.EvalAssertions
}

// Run evaluates selected local agents without making ordinary CI tests invoke them.
func Run(ctx context.Context, options Options) (Report, error) {
	if options.ReconifyPath == "" {
		return Report{}, errors.New("--reconify is required")
	}
	if info, err := os.Stat(options.ReconifyPath); err != nil || info.IsDir() {
		return Report{}, fmt.Errorf("reconify binary %q is not an executable file", options.ReconifyPath)
	}
	if options.Trials <= 0 {
		return Report{}, errors.New("--trials must be positive")
	}
	if len(options.Agents) == 0 {
		return Report{}, errors.New("--agent is required")
	}
	if options.Timeout <= 0 {
		options.Timeout = 10 * time.Minute
	}
	if options.LookPath == nil {
		options.LookPath = exec.LookPath
	}
	agents, skipped, err := resolveAgents(options.Agents, options.LookPath)
	if err != nil {
		return Report{}, err
	}
	scenarios, err := loadScenarios(options.CorpusDir, options.ScenarioIDs)
	if err != nil {
		return Report{}, err
	}
	report := Report{Schema: reportSchemaV1, Skipped: skipped}
	for _, agent := range agents {
		current := AgentReport{Agent: string(agent)}
		for _, item := range scenarios {
			entry := ScenarioReport{ID: item.ID, Trials: make([]TrialReport, options.Trials)}
			var group sync.WaitGroup
			for trial := 1; trial <= options.Trials; trial++ {
				group.Add(1)
				go func(index int) {
					defer group.Done()
					trialCtx, cancel := context.WithTimeout(ctx, options.Timeout)
					entry.Trials[index-1] = runTrial(trialCtx, options, agent, item, index)
					cancel()
				}(trial)
			}
			group.Wait()
			entry.Classification = metrics(entry.Trials, func(result TrialReport) bool { return result.Classification })
			entry.ExactResult = metrics(entry.Trials, func(result TrialReport) bool { return result.ExactResult })
			current.Scenarios = append(current.Scenarios, entry)
		}
		report.Agents = append(report.Agents, current)
	}
	return report, nil
}

func metrics(trials []TrialReport, passed func(TrialReport) bool) Metrics {
	result := Metrics{PassAll: len(trials) > 0}
	if len(trials) > 0 {
		result.PassAt1 = passed(trials[0])
	}
	for _, trial := range trials {
		if !passed(trial) {
			result.PassAll = false
		}
	}
	return result
}

func resolveAgents(values []string, lookPath func(string) (string, error)) ([]Agent, []SkippedAgent, error) {
	all, requested := false, map[Agent]bool{}
	for _, value := range values {
		if value == "all" {
			all = true
			continue
		}
		agent := Agent(value)
		if !containsAgent(supportedAgents(), agent) {
			return nil, nil, fmt.Errorf("unsupported agent %q", value)
		}
		requested[agent] = true
	}
	if all {
		for _, agent := range supportedAgents() {
			requested[agent] = true
		}
	}
	if len(requested) == 0 {
		return nil, nil, errors.New("--agent must name an agent or all")
	}
	var available []Agent
	var skipped []SkippedAgent
	for _, agent := range supportedAgents() {
		if !requested[agent] {
			continue
		}
		if _, err := lookPath(string(agent)); err != nil {
			if all {
				skipped = append(skipped, SkippedAgent{Agent: string(agent), Reason: "not installed or not on PATH"})
				continue
			}
			return nil, nil, fmt.Errorf("selected agent %q is unavailable: %w", agent, err)
		}
		available = append(available, agent)
	}
	return available, skipped, nil
}

func containsAgent(agents []Agent, target Agent) bool {
	for _, agent := range agents {
		if agent == target {
			return true
		}
	}
	return false
}

func loadScenarios(corpusDir string, only []string) ([]scenario, error) {
	if corpusDir == "" {
		corpusDir = "evals"
	}
	wanted := map[string]bool{}
	for _, id := range only {
		wanted[id] = true
	}
	entries, err := os.ReadDir(corpusDir)
	if err != nil {
		return nil, fmt.Errorf("read corpus: %w", err)
	}
	var found []scenario
	for _, entry := range entries {
		if !entry.IsDir() || (len(wanted) > 0 && !wanted[entry.Name()]) {
			continue
		}
		path := filepath.Join(corpusDir, entry.Name(), "scenario.json")
		data, err := os.ReadFile(path) // #nosec G304 -- checked-in corpus path.
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		var header struct {
			Schema string `json:"schema"`
		}
		if err := json.Unmarshal(data, &header); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		if header.Schema == schemas.EvalScenarioSchemaV1 {
			var document schemas.EvalScenario
			if err := json.Unmarshal(data, &document); err != nil {
				return nil, fmt.Errorf("parse %s: %w", path, err)
			}
			found = append(found, scenario{ID: document.ID, Prompt: document.Prompt, Inputs: document.Inputs, ExpectedResult: document.ExpectedResult, Pair: document.Pair, Assertions: document.Assertions, Dir: filepath.Join(corpusDir, entry.Name())})
			continue
		}
		if header.Schema != schemas.EvalScenarioSchemaV2 {
			return nil, fmt.Errorf("scenario %s has unsupported schema %q", entry.Name(), header.Schema)
		}
		var document schemas.EvalScenarioV2
		if err := json.Unmarshal(data, &document); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		found = append(found, scenario{ID: document.ID, Prompt: document.Prompt, Inputs: document.Inputs, ExpectedResult: document.ExpectedResult, ExpectedExplanation: document.ExpectedExplanation, Pair: document.Pair, Assertions: document.Assertions, Dir: filepath.Join(corpusDir, entry.Name())})
	}
	if len(found) == 0 {
		return nil, errors.New("no requested scenarios found")
	}
	sort.Slice(found, func(i, j int) bool { return found[i].ID < found[j].ID })
	return found, nil
}

func runTrial(ctx context.Context, options Options, agent Agent, item scenario, trial int) TrialReport {
	report := TrialReport{Trial: trial}
	workspace, cleanup, err := materialize(options, item)
	if err != nil {
		report.Error = err.Error()
		return report
	}
	defer cleanup()
	output, agentErr := runAgent(ctx, agent, workspace, taskPrompt(item), options.ReconifyPath)
	report.AgentOutput = boundAgentOutput(output)
	if agentErr != nil {
		report.Error = agentErr.Error()
	}
	commands, readErr := readCommands(filepath.Join(workspace, ".reconify-eval-commands.log"))
	if readErr != nil && report.Error == "" {
		report.Error = readErr.Error()
	}
	report.Commands = commands
	report.Discovery = containsCommand(commands, "capabilities")
	config := filepath.Join(workspace, "reconify.yaml")
	if !fileExists(config) {
		if report.Error == "" {
			report.Error = "agent did not create reconify.yaml"
		}
		return report
	}
	report.Configuration = containsCommand(commands, "config validate") && runReconify(ctx, options.ReconifyPath, workspace, "config", "validate", "--config", config) == nil
	verified := filepath.Join(workspace, "verified-result.json")
	if err := runReconify(ctx, options.ReconifyPath, workspace, "reconcile", "--config", config, "--pair", item.Pair, "--format", "json", "--deterministic", "--out", verified); err != nil {
		if report.Error == "" {
			report.Error = fmt.Sprintf("verify reconciliation: %v", err)
		}
		return report
	}
	report.Execution = containsCommand(commands, "reconcile") && fileExists(filepath.Join(workspace, "agent-result.json"))
	actual, _ := os.ReadFile(verified)                                       // #nosec G304 -- evaluator workspace.
	expected, _ := os.ReadFile(filepath.Join(item.Dir, item.ExpectedResult)) // #nosec G304 -- checked-in fixture.
	report.ExactResult = bytes.Equal(bytes.TrimSpace(actual), bytes.TrimSpace(expected))
	report.Classification = report.ExactResult || assertionsMatch(actual, item.Assertions)
	if item.ExpectedExplanation == "" {
		return report
	}
	agentExplanation, err := os.ReadFile(filepath.Join(workspace, "agent-explanation.json")) // #nosec G304 -- evaluator workspace.
	if err == nil {
		expectedExplanation, readErr := os.ReadFile(filepath.Join(item.Dir, item.ExpectedExplanation)) // #nosec G304 -- checked-in fixture.
		report.Explanation = readErr == nil && containsCommand(commands, "explain") && bytes.Equal(bytes.TrimSpace(agentExplanation), bytes.TrimSpace(expectedExplanation))
	}
	return report
}

const maxAgentOutputBytes = 40000

func boundAgentOutput(output []byte) string {
	if len(output) <= maxAgentOutputBytes {
		return string(output)
	}
	return string(output[:maxAgentOutputBytes]) + "\n[agent output truncated]"
}

func assertionsMatch(data []byte, expected schemas.EvalAssertions) bool {
	var document struct {
		Summary schemas.EvalAssertions `json:"summary"`
	}
	return json.Unmarshal(data, &document) == nil && document.Summary == expected
}

func materialize(options Options, item scenario) (string, func(), error) {
	workspace, err := os.MkdirTemp("", "reconify-eval-")
	if err != nil {
		return "", nil, fmt.Errorf("create workspace: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(workspace) }
	for _, input := range item.Inputs {
		if err := copyFile(filepath.Join(item.Dir, input), filepath.Join(workspace, input)); err != nil {
			cleanup()
			return "", nil, err
		}
	}
	for _, dir := range []string{".agents", ".claude", ".codex"} {
		if err := copyTree(filepath.Join(options.SkillsDir, dir), filepath.Join(workspace, dir, "skills")); err != nil && !errors.Is(err, os.ErrNotExist) {
			cleanup()
			return "", nil, err
		}
	}
	if err := writeWrapper(workspace); err != nil {
		cleanup()
		return "", nil, err
	}
	if err := exec.Command("git", "init", "--quiet", workspace).Run(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("initialize temporary git workspace: %w", err)
	} // #nosec G204 -- fixed command.
	return workspace, cleanup, nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src) // #nosec G304 -- checked-in fixture path.
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o600) // #nosec G703 -- evaluator workspace.
}

func copyTree(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		from, to := filepath.Join(src, entry.Name()), filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			if err := copyTree(from, to); err != nil {
				return err
			}
			continue
		}
		if err := copyFile(from, to); err != nil {
			return err
		}
	}
	return nil
}

func writeWrapper(workspace string) error {
	bin := filepath.Join(workspace, ".bin")
	if err := os.MkdirAll(bin, 0o750); err != nil {
		return err
	}
	data := []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$RECONIFY_EVAL_LOG\"\nexec \"$RECONIFY_EVAL_BINARY\" \"$@\"\n")
	return os.WriteFile(filepath.Join(bin, "reconify"), data, 0o700) // #nosec G306,G703 -- executable wrapper in evaluator workspace.
}

func taskPrompt(item scenario) string {
	return fmt.Sprintf("%s\n\nWork only in this temporary workspace and stay at its root. Follow the installed Reconify Engine skill under .agents/skills. Complete and verify this ordered workflow: (1) run `reconify capabilities`; (2) discover the actual input paths and run `reconify inspect` on every input; (3) run `reconify config schema`; (4) write reconify.yaml at the workspace root using paths relative to that file; (5) run `reconify config validate --config reconify.yaml`; (6) run `reconify config check-source` for every configured source; (7) reconcile pair %q with `--format json --deterministic --out agent-result.json`; (8) run `reconify explain agent-result.json > agent-explanation.json`. After each step, verify its output or artifact before continuing. If a command fails because of a wrong path, flag, schema key, or config value, read the error, correct it, and rerun the failed step. Do not stop after creating YAML or after reconciliation: leave reconify.yaml, agent-result.json, and agent-explanation.json in the workspace.", item.Prompt, item.Pair)
}

func runReconify(ctx context.Context, binary, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, binary, args...) // #nosec G204 -- binary is explicit CLI input.
	cmd.Dir = dir
	return cmd.Run()
}

func readCommands(path string) ([]string, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- evaluator workspace.
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return strings.FieldsFunc(string(data), func(r rune) bool { return r == '\n' }), nil
}

func containsCommand(commands []string, fragment string) bool {
	for _, command := range commands {
		if strings.Contains(command, fragment) {
			return true
		}
	}
	return false
}
func fileExists(path string) bool { _, err := os.Stat(path); return err == nil }

// WriteReport writes formatted JSON to stdout or an explicit output path.
func WriteReport(report Report, output string, stdout io.Writer) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if output == "" {
		_, err = stdout.Write(data)
		return err
	}
	return os.WriteFile(output, data, 0o600) // #nosec G304 -- explicit output flag.
}
