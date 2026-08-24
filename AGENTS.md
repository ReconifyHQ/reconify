# Reconify Agent Guide

Reconify is a Go CLI and library for reconciling financial CSV data across systems such as banks, PSPs, ledgers, and spreadsheets.

## Start Here

- Read `README.md` for user-facing setup and CLI examples.
- Read `docs/content/docs/cli/engine/index.md` before changing matching behavior.
- Read `docs/content/docs/cli/performance/index.md` before changing streaming, indexing, or large-file behavior.
- Read `docs/architecture/partitioned-parallelism.md` before changing partition workers, result chunks, carry-forward, or queue behavior.
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
- `--result-mode` controls event emission: `all` (default), `exceptions_only` (suppress clean matches), `summary_only` (suppress all item events). Can also be set per-pair as `result_mode` in YAML. CLI flag overrides pair config.

## Agent Skills

Canonical skills live under `.agents/skills/` and are intentionally tool-agnostic:

- `.agents/skills/reconify-engine-reconcile/SKILL.md` — the full discover → configure → reconcile → explain workflow
- `.agents/skills/reconify-engine-config/SKILL.md` — YAML config creation, validation, source/pair setup
- `.agents/skills/reconify-engine-cli/SKILL.md` — commands, flags, output formats, exit codes
- `.agents/skills/reconify-engine-debug/SKILL.md` — interpreting result events, diagnosing mismatches
- `.agents/skills/reconify-engine-bootstrap/SKILL.md` — end-to-end new project setup from scratch
- `.agents/skills/reconify-engine-ci/SKILL.md` — deterministic artifacts, exit-code policy
- `.agents/skills/reconify-engine-performance/SKILL.md` — index backends, streaming, large files

`reconify-engine-reconcile` carries the routing table: agents start there unless the task is clearly
bootstrap, config, debug, performance, or CI work, and it delegates for detail rather than handing
off the whole task. Keep that table and `skills/README.md` in sync when skills are added or renamed.

The skills produce `reconify.yaml`, `result.json`, and `explanation.json` at the workspace root.
This naming is the public convention; do not introduce evaluator-specific filenames into it.

The former `reconify-*` names remain installed as deprecated adapters that redirect to these.

These skills are written for an agent holding an installed `reconify` binary and someone else's
files. They must not reference repository paths such as `config/config.go` or `docs/`, because
`npx @reconifyhq/skills` ships them into projects where those paths do not exist. Route agents to
the self-describing surface instead — `reconify capabilities`, `reconify inspect`, and
`reconify config schema`. Contributor-facing guidance belongs in this file.

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

## Test Organization

Keep tests with the package that owns the behavior. For a narrow unit of
implementation, prefer an exact file pair: `filename.go` and
`filename_test.go` in the same directory. This is required when the test needs
access to unexported implementation details.

Use a behavior-oriented test filename instead when one test deliberately spans
multiple implementation files or validates a public workflow. For example,
format, parity, partitioned, multi-source, and telemetry workflow tests should
remain focused package-level suites rather than being forced into an arbitrary
one-to-one filename pairing. Shared package-local fixtures belong in a clearly
named `test_helpers_test.go` file.
