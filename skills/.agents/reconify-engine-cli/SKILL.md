---
name: reconify-engine-cli
description: Change, document, or verify Reconify Engine CLI commands, flags, and machine-readable output.
---

# Reconify Engine CLI

Use this workflow for `cmd/reconify`, `internal/cli`, CLI documentation, or output formats.

1. Inspect the live help for every command you change before editing docs or flags.
2. Keep result data on stdout or `--out`; keep diagnostics and progress on stderr.
3. Preserve explicit-flag precedence and additive output compatibility.
4. Add focused Cobra tests, then run `go test ./...`. Run docs type/build checks when user-facing docs change.

Key entry points: `internal/cli/root.go`, `internal/cli/reconcile.go`, `internal/cli/schema.go`, and `engine/format.go`.
