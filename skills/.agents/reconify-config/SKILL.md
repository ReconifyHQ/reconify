---
name: reconify-config
description: Create, validate, review, or document Reconify YAML configuration. Use when an agent works with reconify.yaml, examples/reconify.yaml, config/config.go, source parser mappings, pair rules, index backends, or config-related CLI commands.
---

# Reconify Config

## Overview

Use this skill when the task involves Reconify configuration shape, validation, examples, or source/pair setup. The source of truth is the Go model in `config/config.go`.

## Source Of Truth

- Config structs and validation: `config/config.go`
- User example: `examples/reconify.yaml`
- README quick-start config: `README.md`
- Config CLI checks: `internal/cli/config.go`

## Required Shape

Every valid config needs:

- `version: 1`
- `sources` with at least one source
- each source: `file_pattern` and a CSV `parser`
- each parser: `type: csv`, `date_col`, `date_layout`, `amount_col`, and positive `multiplier`
- each pair: `left`, `right`, optional `date_window`, non-negative `amount_tolerance_minor`, `name_mode` of `none` or `tokens`, and optional `result_mode` of `all`, `exceptions_only`, or `summary_only`

Optional performance-related fields:

- `index.backend`: `memory`, `disk`, or `auto`
- `index.spill_dir`: directory for disk index temporary files
- `index.auto_max_right_file_mb`: threshold for `auto`
- `parser.skip_raw`: skip Raw map allocation on parsed transactions

## Workflow

1. Read `config/config.go` before adding fields or changing validation behavior.
2. Keep YAML examples aligned with struct tags and validation rules.
3. Validate structure with:

```bash
go run ./cmd/reconify config validate --config examples/reconify.yaml
```

4. When checking a real CSV, use:

```bash
go run ./cmd/reconify config check-source --config reconify.yaml --source bank --file data/bank/jan.csv
```

5. If a field is added, update `examples/reconify.yaml`, README snippets, and tests together.

## YAML Guidance

- Use source names that describe systems: `bank`, `stripe`, `ledger`.
- Keep amounts in minor units after parsing; use `multiplier: 100` for decimal currencies.
- Use Go time layouts such as `2006-01-02`, not strftime-style layouts.
- Use `name_mode: tokens` only when reference matching alone is insufficient.
- Use `index.backend: auto` for large right-side files where memory pressure is a concern.

Do not invent config keys without adding typed fields and validation.
