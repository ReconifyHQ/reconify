---
name: reconify-engine-cli
description: Resolve Reconify CLI behavior from the installed binary. Use when choosing commands, flags, formats, schemas, or exit-code handling.
---

# Reconify Engine CLI

## Resolve the installed contract

```bash
reconify capabilities
reconify COMMAND --help
reconify schema NAME
reconify config schema
```

Start with `capabilities`, then load only the help or schema for the branch being implemented. The
CLI decision is complete when the chosen command, flags, format, schema identifier, and exit policy
all appear in the installed contract.

## Keep streams separate

Result data goes to stdout or `--out`. Diagnostics, warnings, validation messages, and progress go
to stderr. Use `--error-format json` when a caller needs the
`reconify.engine.diagnostic.v1` contract, and branch on documented exit codes rather than message
text.

## Choose the retained artifact deliberately

| Need | Choose |
|---|---|
| Complete, stable document for review or diffing | `json`, `--result-mode all`, `--deterministic` |
| Bounded-memory event stream | `ndjson` or `csv` |
| Lower encoding pressure while retaining one JSON document | `json-stream` |
| Interactive inspection of a small result | `table` |

`json` and `table` buffer the result. `ndjson` and `csv` remain bounded with result size;
`json-stream` releases encoded structs early but still accumulates one JSON document. Confirm the
chosen behavior against `capabilities` and command help for the installed build.

## Understand the agent profile

`--agent` selects machine-readable defaults and exception-focused result emission. It fits callers
that consume differences as events. A retained complete result uses explicit `--format` and
`--result-mode all`; explicit flags override the profile defaults.

CLI work is complete when a representative invocation produces data in the declared format,
diagnostics remain on stderr, and the caller handles every exit code it opted into.
