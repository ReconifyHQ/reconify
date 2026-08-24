package evals

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Agent identifies a supported local coding-agent CLI.
type Agent string

// Supported local coding-agent CLIs.
const (
	AgentClaude   Agent = "claude"
	AgentCodex    Agent = "codex"
	AgentGemini   Agent = "gemini"
	AgentOpenCode Agent = "opencode"
)

func supportedAgents() []Agent {
	return []Agent{AgentClaude, AgentCodex, AgentGemini, AgentOpenCode}
}

func agentCommand(ctx context.Context, agent Agent, workspace, prompt string) *exec.Cmd {
	return agentCommandWithModel(ctx, agent, workspace, prompt, "")
}

func agentCommandWithModel(ctx context.Context, agent Agent, workspace, prompt, model string) *exec.Cmd {
	modelArgs := func(args []string, flag string) []string {
		if model != "" {
			args = append(args, flag, model)
		}
		return args
	}
	switch agent {
	case AgentClaude:
		args := modelArgs([]string{"-p", "--output-format", "json", "--permission-mode", "acceptEdits"}, "--model")
		args = append(args, prompt)
		return exec.CommandContext(ctx, "claude", args...) // #nosec G204 -- fixed adapter command.
	case AgentCodex:
		args := modelArgs([]string{"exec", "-C", workspace, "--sandbox", "workspace-write"}, "-m")
		args = append(args, prompt)
		return exec.CommandContext(ctx, "codex", args...) // #nosec G204 -- fixed adapter command.
	case AgentGemini:
		args := modelArgs([]string{"--prompt", prompt, "--output-format", "json", "--approval-mode", "auto_edit", "--skip-trust"}, "--model")
		return exec.CommandContext(ctx, "gemini", args...) // #nosec G204 -- fixed adapter command.
	default:
		args := modelArgs([]string{"run", "--dir", workspace, "--auto"}, "--model")
		args = append(args, prompt)
		return exec.CommandContext(ctx, "opencode", args...) // #nosec G204 -- fixed adapter command.
	}
}

func runAgent(ctx context.Context, agent Agent, workspace, prompt, reconifyPath, model string) ([]byte, error) {
	cmd := agentCommandWithModel(ctx, agent, workspace, prompt, model)
	cmd.Dir = workspace
	cmd.Env = append(os.Environ(),
		"PATH="+filepath.Join(workspace, ".bin")+string(os.PathListSeparator)+os.Getenv("PATH"),
		"RECONIFY_EVAL_LOG="+filepath.Join(workspace, ".reconify-eval-commands.log"),
		"RECONIFY_EVAL_BINARY="+reconifyPath,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("%s: %w", agent, err)
	}
	return output, nil
}
