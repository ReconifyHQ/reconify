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

Start at `reconify-engine-reconcile` unless the situation clearly matches another row:

| Situation | Skill |
|---|---|
| Unfamiliar files, or one task spanning discovery through explanation | `reconify-engine-reconcile` |
| No `reconify.yaml` yet, and the outcome is a first validated config plus a first result | `reconify-engine-bootstrap` |
| A `reconify.yaml` exists and needs creation detail, correction, or validation | `reconify-engine-config` |
| A run succeeded but the counters contradict the business scenario | `reconify-engine-debug` |
| Memory, wall time, or artifact size is the binding constraint | `reconify-engine-performance` |
| The workflow must run unattended with an exit-code policy | `reconify-engine-ci` |
| A question about commands, flags, formats, or exit codes | `reconify-engine-cli` |

`reconify-engine-reconcile` owns the end-to-end sequence and delegates to the others for
configuration detail, diagnosis, and large inputs rather than handing off the whole task.

## Artifacts

The skills produce a consistent set of files at the workspace root:

```
reconify.yaml       the validated config; commit it
result.json         the retained reconciliation artifact; regenerate it
explanation.json    the explanation of that result; regenerate it alongside
```

The former `reconify-*` names remain available as explicit-invocation compatibility aliases. They
route to the canonical `reconify-engine-*` workflows and stay out of automatic skill selection.

## Repository

[github.com/reconifyhq/reconify](https://github.com/reconifyhq/reconify)
