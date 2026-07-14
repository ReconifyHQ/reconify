# Reconify Agent Guide

Reconify is a Go CLI and library for reconciling financial CSV data across systems such as banks, PSPs, ledgers, and spreadsheets.

## Start Here

- Read `README.md` for user-facing setup and CLI examples.
- Read `docs/content/docs/engine/index.md` before changing matching behavior.
- Read `docs/content/docs/performance/index.md` before changing streaming, indexing, or large-file behavior.
- Use canonical agent skills in `.agents/skills/` for repeatable workflows.

## Repo Map

- `cmd/reconify/`: CLI entrypoint.
- `internal/cli/`: Cobra commands and flags.
- `config/`: YAML config loading and validation.
- `engine/`: parser, reconciliation engine, indexes, output writers, audit data.
- `examples/reconify.yaml`: baseline config example.
- `scripts/`: benchmark and performance data helpers.

## Commands

```bash
go mod download
go run ./cmd/reconify --help
go run ./cmd/reconify reconcile --help
go test ./...
make test
make lint
make security
make build
make check
make preflight
```

Use `go test ./...` for quick verification. Use `make test` when race detection and coverage output are needed. **After any code change and before opening a PR, run `make check`.** It is the local equivalent of the GitHub Actions quality gate: dependency drift checks, formatting checks, linting, security scans, race-tested coverage, build, and smoke benchmarks. `make preflight` remains an alias for compatibility.

## CLI Conventions

- Data output goes to stdout or `--out`.
- Status, progress, and validation messages go to stderr.
- `parse` formats: `ndjson`, `csv`, `table`, `json`.
- `reconcile` formats: `json`, `json-stream`, `ndjson`, `csv`, `table`.
- Prefer `ndjson` or `csv` for large reconciliation jobs.

## Agent Skills

Canonical skills live under `.agents/skills/` and are intentionally tool-agnostic:

- `.agents/skills/reconify-cli/SKILL.md` — CLI commands, flags, output formats, docs
- `.agents/skills/reconify-config/SKILL.md` — YAML config creation, validation, source/pair setup
- `.agents/skills/reconify-performance/SKILL.md` — benchmarking, streaming, index backends
- `.agents/skills/reconify-debug/SKILL.md` — interpreting NDJSON/JSON output, diagnosing mismatches
- `.agents/skills/reconify-bootstrap/SKILL.md` — end-to-end new project setup from scratch
- `.agents/skills/reconify-ci/SKILL.md` — GitHub Actions, drift detection, --fail-if-unmatched

Tool-specific files should be thin adapters that point back to these canonical skills. Do not duplicate long workflow instructions across Codex, Claude, Gemini, and Copilot files.

**Installing skills into another project:** `npx @reconifyhq/skills` copies all skill files into the target project's `.agents/skills/`, `.claude/skills/`, and `.codex/skills/` directories. The npm package is defined in `package.json` at the repo root; the install script is `scripts/install-skills.js`.

## Pull Requests

- Use the template at `.github/PULL_REQUEST_TEMPLATE.md`. GitHub pre-fills it
  automatically when opening a PR via the UI or `gh pr create`.
- Fill it out instead of writing a free-form description.

## Change Rules

- Keep edits scoped to the requested behavior.
- Do not rewrite public config keys, CLI flags, or output formats unless the task explicitly requires a breaking change.
- Update README/examples/tests when user-facing CLI or config behavior changes.
- Do not commit generated benchmark datasets, local CSVs, coverage files, binaries, or private `*.local.yaml` configs.
