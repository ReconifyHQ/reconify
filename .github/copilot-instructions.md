# Copilot Instructions

Follow `AGENTS.md` for Reconify project context, commands, and safety rules.

Use `.agents/skills/` as the canonical workflow source:

- `.agents/skills/reconify-cli/SKILL.md` for CLI command and docs work.
- `.agents/skills/reconify-config/SKILL.md` for YAML config work.
- `.agents/skills/reconify-performance/SKILL.md` for benchmark, memory, and streaming work.

Prefer exact repo-root commands from `AGENTS.md`. Keep generated files, binaries, local CSVs, coverage output, and private `*.local.yaml` configs out of commits.
