# Copilot Instructions

Follow `AGENTS.md` for Reconify project context, commands, and safety rules.
After any code change and before opening a PR, run `make check`.

Use `.agents/skills/` as the canonical workflow source:

- `.agents/skills/reconify-engine-reconcile/SKILL.md` for end-to-end Engine workflows.
- `.agents/skills/reconify-engine-cli/SKILL.md` for CLI command and docs work.
- `.agents/skills/reconify-engine-config/SKILL.md` for YAML config work.
- `.agents/skills/reconify-engine-performance/SKILL.md` for benchmark, memory, and streaming work.

Prefer exact repo-root commands from `AGENTS.md`. Keep generated files, binaries, local CSVs, coverage output, and private `*.local.yaml` configs out of commits.
