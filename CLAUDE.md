# Claude Instructions

Read `AGENTS.md` first. It is the canonical project orientation for Reconify.
After any code change and before opening a PR, run `make check`.

Use the reusable workflows in `.agents/skills/` when the task touches the CLI, config files, or performance work. Claude-specific skill adapters live in `.claude/skills/` and intentionally redirect to the canonical `.agents/skills/` files.

Keep this file short. Project rules belong in `AGENTS.md`; reusable procedures belong in `.agents/skills/`.
