---
name: reconify-engine-cli
description: Reconify Engine command surface — commands, flags, output formats, exit codes, and the machine-readable schemas each command emits.
---

# Reconify Engine CLI

Run `reconify capabilities` first. It reports this build's protocol version, every available
command, and the schema identifiers it emits. It is authoritative for the binary in front of you
and cheaper than guessing from memory.

## Commands

| Command | Purpose |
|---|---|
| `capabilities` | Describe the installed interface and its schemas |
| `inspect FILE` | Profile a file's format, column types, and date layouts |
| `config infer --left F --right F` | Confidence-gated config proposal |
| `config schema` | Typed description of every config key — the config source of truth |
| `config validate` | Validate a `reconify.yaml` |
| `config check-source` | Prove a source mapping resolves against a real file |
| `reconcile --pair NAME` | Run a reconciliation |
| `explain RESULT` | Deterministic, bounded summary of a result file (stdout only) |
| `parse` | Parse one file through a configured source parser |
| `schema <name>` | Print a published JSON Schema |

`config init` is interactive. Do not call it from a non-interactive session; write the YAML
directly instead.

## Exit codes

| Code | Meaning |
|---|---|
| `0` | Success |
| `1` | Unexpected or internal error |
| `2` | Config or validation error — bad YAML, missing pair or source, column not found |
| `3` | Unmatched rows remained — **only** when `--fail-if-unmatched` is set |
| `4` | Exception events occurred — **only** when `--fail-if-exceptions` is set; takes precedence over `3` |

Codes `3` and `4` are opt-in policy, not failures. A reconciliation that finds unmatched rows exits
`0` unless you asked for otherwise. Branch on exit codes, never on message text.

## Output

`reconcile` supports `json` (default), `json-stream`, `ndjson`, `csv`, and `table`. The streaming
formats are `ndjson`, `json-stream`, and `csv` — use one of them for large inputs.

- `--deterministic` sorts output sections for stable diffs. It applies to `json` only.
- `--audit` embeds provenance: SHA-256 file hashes, timestamp, tool version, pair config snapshot.
  Pair it with `--audit-fixed-timestamp` for byte-identical reruns.
- `--result-mode` selects `all` (default), `exceptions_only`, or `summary_only`.
- `--out` writes result data to a file; `-` means stdout. `reconcile` and `parse` accept it;
  `explain` does not — it writes to stdout, so redirect it: `reconify explain result.json > explanation.json`.
  `explain --top N` bounds the exception events included (default `10`).

Result data goes to stdout or `--out`. Diagnostics, warnings, and progress go to stderr. Keep the
two separated when scripting, and use `--error-format json` to receive errors as
`reconify.engine.diagnostic.v1` on stderr.

## What --agent actually does

`--agent` selects machine-readable defaults: streaming event output **and**
`result_mode: exceptions_only`, which suppresses clean matches. It suits exception-driven
pipelines. It is not the way to produce a complete result document — a result written under
`--agent` has no `matched` array.

When you need the whole document, ask for it explicitly:

```bash
reconify reconcile --config reconify.yaml --pair PAIR --format json --deterministic --out result.json
```

`--agent --format json --result-mode all` gives agent defaults with a complete document.
