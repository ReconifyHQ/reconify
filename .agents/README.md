# Agent Workflows

This directory contains tool-agnostic workflow instructions for AI coding agents working on Reconify.

The canonical skills are:

- `skills/reconify-cli/SKILL.md`
- `skills/reconify-config/SKILL.md`
- `skills/reconify-performance/SKILL.md`

Tool-specific integrations should point back here:

- `AGENTS.md` gives project orientation.
- `CLAUDE.md`, `GEMINI.md`, and `.github/copilot-instructions.md` are short compatibility shims.
- `.claude/skills/` and `.codex/skills/` contain thin skill adapters.

Update canonical skills first. Only update adapters when a tool needs a different discovery path.

For any code change and before opening a pull request, agents must run `make check`.
`AGENTS.md` defines the command and its GitHub Actions-equivalent scope.
