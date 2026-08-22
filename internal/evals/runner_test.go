package evals

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveAgentsSkipsUnavailableOnlyForAll(t *testing.T) {
	lookPath := func(name string) (string, error) {
		if name == "codex" {
			return "", errors.New("missing")
		}
		return "/bin/" + name, nil
	}
	agents, skipped, err := resolveAgents([]string{"all"}, lookPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 3 || len(skipped) != 1 || skipped[0].Agent != "codex" {
		t.Fatalf("agents=%v skipped=%+v", agents, skipped)
	}
	if _, _, err := resolveAgents([]string{"codex"}, lookPath); err == nil {
		t.Fatal("selected unavailable agent succeeded")
	}
}

func TestRunValidatesRequiredOptions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reconify")
	if err := os.WriteFile(path, []byte("binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), Options{ReconifyPath: path, Agents: []string{"codex"}}); err == nil {
		t.Fatal("missing trials succeeded")
	}
	if _, err := Run(context.Background(), Options{ReconifyPath: path, Trials: 1}); err == nil {
		t.Fatal("missing agent succeeded")
	}
}

func TestMetrics(t *testing.T) {
	got := metrics([]TrialReport{{Classification: false}, {Classification: true}}, func(report TrialReport) bool { return report.Classification })
	if got.PassAt1 || got.PassAll {
		t.Fatalf("metrics=%+v", got)
	}
}

func TestMaterializeInstallsSkillsAtPublishedPaths(t *testing.T) {
	root := t.TempDir()
	skills := filepath.Join(root, "skills")
	for _, dir := range []string{".agents", ".claude", ".codex"} {
		if err := os.MkdirAll(filepath.Join(skills, dir, "reconify-engine-reconcile"), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(skills, dir, "reconify-engine-reconcile", "SKILL.md"), []byte("skill"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	scenarioDir := filepath.Join(root, "scenario")
	if err := os.MkdirAll(filepath.Join(scenarioDir, "inputs"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scenarioDir, "inputs", "left.csv"), []byte("id\n1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace, cleanup, err := materialize(Options{SkillsDir: skills}, scenario{Dir: scenarioDir, Inputs: []string{"inputs/left.csv"}})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	for _, dir := range []string{".agents", ".claude", ".codex"} {
		path := filepath.Join(workspace, dir, "skills", "reconify-engine-reconcile", "SKILL.md")
		if !fileExists(path) {
			t.Fatalf("skill missing at %s", path)
		}
	}
}

func TestLoadScenariosSupportsPublishedV1(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "legacy")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	fixture := `{"schema":"reconify.engine.eval-scenario.v1","id":"legacy","prompt":"p","inputs":[],"reference_config":"reference/reconify.yaml","expected_result":"expected/result.json","pair":"pair","assertions":{"matched":0,"unmatched_left":0,"unmatched_right":0,"amount_diff_count":0,"timing_diff_count":0,"duplicate_count":0,"grouped_matched_count":0,"many_to_many_matched_count":0,"ambiguous_group_count":0},"counter_examples":[]}`
	if err := os.WriteFile(filepath.Join(dir, "scenario.json"), []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	scenarios, err := loadScenarios(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(scenarios) != 1 || scenarios[0].ExpectedExplanation != "" {
		t.Fatalf("scenarios=%+v", scenarios)
	}
}

func TestAgentCommandsUseIsolatedWorkspace(t *testing.T) {
	workspace := t.TempDir()
	for _, agent := range supportedAgents() {
		cmd := agentCommand(context.Background(), agent, workspace, "test")
		if cmd.Path == "" {
			t.Fatalf("%s has no command", agent)
		}
		if agent != AgentClaude && agent != AgentGemini && !containsString(cmd.Args, workspace) {
			t.Fatalf("%s does not receive workspace: %v", agent, cmd.Args)
		}
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
