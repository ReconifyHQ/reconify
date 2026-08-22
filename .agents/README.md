# Agent Workflows

This directory contains tool-agnostic workflow instructions for AI coding agents working on Reconify.

The canonical skills are:

- `skills/reconify-engine-reconcile/SKILL.md`
- `skills/reconify-engine-cli/SKILL.md`
- `skills/reconify-engine-config/SKILL.md`
- `skills/reconify-engine-debug/SKILL.md`
- `skills/reconify-engine-performance/SKILL.md`
- `skills/reconify-engine-bootstrap/SKILL.md`
- `skills/reconify-engine-ci/SKILL.md`

Tool-specific integrations should point back here:

- `AGENTS.md` gives project orientation.
- `CLAUDE.md`, `GEMINI.md`, and `.github/copilot-instructions.md` are short compatibility shims.
- `.claude/skills/` and `.codex/skills/` contain thin skill adapters.

Update canonical Engine skills first. The former `reconify-*` names are deprecated adapters.

For any code change and before opening a pull request, agents must run `make check`.
`AGENTS.md` defines the command and its GitHub Actions-equivalent scope.
