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
| `reconify-engine-reconcile` | End-to-end discovery, configuration, reconciliation, and explanation |
| `reconify-engine-cli` | CLI commands, flags, and output formats |
| `reconify-engine-config` | YAML configuration and validation |
| `reconify-engine-debug` | Result artifacts and exception diagnosis |
| `reconify-engine-performance` | Streaming, indexes, and benchmarks |
| `reconify-engine-bootstrap` | New reconciliation setup |
| `reconify-engine-ci` | Deterministic CI workflows and exit codes |

The former `reconify-*` names remain available as explicit-invocation compatibility aliases. They
route to the canonical `reconify-engine-*` workflows and stay out of automatic skill selection.

## Repository

[github.com/reconifyhq/reconify](https://github.com/reconifyhq/reconify)
