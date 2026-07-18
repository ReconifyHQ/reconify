---
name: reconify-cli
description: Work with the Reconify command-line interface. Use when an agent needs to inspect CLI commands, update CLI docs, add or change Cobra commands under internal/cli, run smoke checks, explain command behavior, or verify user-facing flags and output formats.
---

# Reconify CLI

## Overview

Use this skill for changes or explanations involving `cmd/reconify`, `internal/cli`, README CLI examples, or generated command output. Prefer inspecting the live Cobra help before documenting behavior.

## Quick Checks

Run the smallest command that proves the current behavior:

```bash
go run ./cmd/reconify --help
go run ./cmd/reconify config --help
go run ./cmd/reconify parse --help
go run ./cmd/reconify reconcile --help
go test ./...
```

Use `make test` only when race detection and coverage output are wanted; it is slower and writes `coverage.out`.

## CLI Map

- Root command: `cmd/reconify/main.go`
- Cobra setup: `internal/cli/root.go`
- Config commands: `internal/cli/config.go`
- Parse command: `internal/cli/parse.go`
- Reconcile command: `internal/cli/reconcile.go`
- Public config model: `config/config.go`
- Output writers: `engine/format.go`

## Workflow

1. Inspect the relevant `--help` output before changing docs or behavior.
2. Trace the command into `internal/cli` and the public package it calls.
3. Preserve existing stdout/stderr conventions: data goes to stdout or `--out`; progress, validation status, and parse counts go to stderr.
4. Keep new flags non-breaking and explicit. Do not change defaults unless the user asks for a behavior change.
5. Add or update CLI tests when behavior changes. For docs-only updates, verify examples still match help text.

## Output Formats

- `parse` supports `ndjson`, `csv`, `table`, and `json`.
- `reconcile` supports `json`, `json-stream`, `ndjson`, `csv`, and `table`.
- For large reconciliation jobs, recommend `ndjson` or `csv`; `json` and `table` buffer more data.
- `--audit` is supported only for structured JSON-style outputs: `json`, `json-stream`, and `ndjson`.

## Result Emission Modes

`--result-mode` controls which reconciliation events appear in the output. Valid values:

| Mode | Emits |
|---|---|
| `all` | Every event — matches, diffs, unmatched, duplicates. **Default.** |
| `exceptions_only` | Exceptions only — unmatched, diffs, duplicates, ambiguous groups. Clean matches suppressed. |
| `summary_only` | Only the final summary. All item events suppressed. |

The mode can also be set per pair in `reconify.yaml` using `result_mode: exceptions_only`. The `--result-mode` CLI flag overrides the pair config when explicitly provided.

Filtering happens at the writer boundary: classification counts and monetary totals in the `summary` always reflect the full reconciliation regardless of the mode.

## Documentation Rules

- Keep examples copy-pasteable from the repo root.
- Prefer `go run ./cmd/reconify ...` in contributor docs and `reconify ...` in user docs.
- If README examples mention imports, use the module path from `go.mod`: `github.com/reconifyhq/reconify`.
