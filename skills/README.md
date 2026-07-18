# @reconifyhq/skills

Agent skill files for [Reconify](https://github.com/reconifyhq/reconify) — installs reusable workflows for Claude Code, Codex, and other AI coding assistants into your project.

## Install

```bash
npx @reconifyhq/skills
```

This copies skill files into your project:

```
.agents/skills/   ← tool-agnostic canonical skills
.claude/skills/   ← Claude Code adapters
.codex/skills/    ← Codex adapters
```

## Skills included

| Skill | Purpose |
|---|---|
| `reconify-cli` | CLI commands, flags, and output formats |
| `reconify-config` | YAML config creation and validation |
| `reconify-debug` | Interpreting NDJSON/JSON output, diagnosing mismatches |
| `reconify-performance` | Benchmarking, streaming, index backends |
| `reconify-bootstrap` | End-to-end new project setup from scratch |
| `reconify-ci` | GitHub Actions, drift detection, `--fail-if-unmatched` |

## Repository

[github.com/reconifyhq/reconify](https://github.com/reconifyhq/reconify)
