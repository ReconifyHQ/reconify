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
	switch agent {
	case AgentClaude:
		return exec.CommandContext(ctx, "claude", "-p", "--output-format", "json", "--permission-mode", "acceptEdits", prompt) // #nosec G204 -- fixed adapter command.
	case AgentCodex:
		return exec.CommandContext(ctx, "codex", "exec", "-C", workspace, "--sandbox", "workspace-write", prompt) // #nosec G204 -- fixed adapter command.
	case AgentGemini:
		return exec.CommandContext(ctx, "gemini", "--prompt", prompt, "--output-format", "json", "--approval-mode", "auto_edit", "--skip-trust") // #nosec G204 -- fixed adapter command.
	default:
		return exec.CommandContext(ctx, "opencode", "run", "--dir", workspace, "--auto", prompt) // #nosec G204 -- fixed adapter command.
	}
}

func runAgent(ctx context.Context, agent Agent, workspace, prompt, reconifyPath string) ([]byte, error) {
	cmd := agentCommand(ctx, agent, workspace, prompt)
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
