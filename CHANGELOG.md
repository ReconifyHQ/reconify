# Changelog

All notable changes to Reconify will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added

- **Global `--agent` execution profile** — defaults errors to structured JSON and reconciliation output to NDJSON with `exceptions_only`, while preserving explicit flag overrides and rejecting interactive commands with a structured alternative ([#107](https://github.com/ReconifyHQ/reconify/issues/107)).
- **`reconify explain`** — reads JSON, JSON-stream, or NDJSON reconciliation results and emits deterministic `reconify.engine.explanation.v1` findings with bounded top exception events; no reconciliation or subjective severity is added.
- **`reconify config infer`** — non-interactively proposes a confidence-gated `reconify.yaml` from two input files. It emits `reconify.engine.config-proposal.v1` with mappings, alternatives, validation counts, and YAML; `--out` writes only ready proposals. `reconify schema config-proposal` prints the published schema.
- **Sample-row validation for `config check-source`** — checks the first 10 data rows by default for date and amount parsing errors. Use `--rows 0` for headers only or set a bounded custom sample size.
- **`reconify inspect FILE`** — deterministically profiles a CSV, JSON, NDJSON, XLSX, or XLSM file before a `reconify.yaml` mapping exists: format, per-column type inference (date layouts, amount formats, ambiguity flags), and representative sample values. Emits `reconify.engine.profile.v1` to stdout (`reconify schema profile` prints the published schema). Scans 1,000 rows by default; `--full` performs an exact scan. `--sample-values` controls how many distinct raw values are included per column (`0` disables). Part of the Reconify Engine Agent Protocol v1 roadmap (AP-5).
- **`--fail-if-exceptions` flag for `reconcile`** — exits with code 4 after a completed run if any `amount_diff`, `timing_diff`, or unmatched event was emitted. Superset of `--fail-if-unmatched`; when both flags are set and both conditions hold, exit code 4 takes precedence over exit code 3. Useful for strict financial pipelines where any discrepancy (not just a missing match) should fail the process.
- **`group_key` column in `parse` CSV and table output** — `parse --format csv` and `parse --format table` now emit `group_key` as the last column, matching the NDJSON and JSON formats. Note: this shifts nothing but adds a trailing column, so scripts reading CSV by column position are unaffected unless they assert on column count.
- **`left_currency` and `right_currency` columns in `csv` result output** — per-row `match`, `amount_diff`, `timing_diff`, and `unmatched_*` events now carry the transaction currency, so multi-currency reconciliations can be read without switching to JSON or NDJSON. Breaking: the columns are inserted after `left_name` / `right_name`, shifting all later columns — scripts reading the CSV by column position must be updated.
- **`config check-source` available columns hint** — when a required column is missing, the command now prints the available column names from the input file's header to stderr, so the user does not have to inspect the file separately.

## [0.4.1] - 2026-07-16

### Added

- **Partition-level parallel reconciliation** — partition workers now process independent partitions concurrently and publish results through a bounded, disk-backed queue while preserving deterministic final-writer ordering.
- **Partition queue controls** — `--partition-workers`, `--partition-queue-capacity`, and `--partition-max-chunk-mb` configure concurrency and temporary result chunk limits for the partitioned backend.
- **Partitioned parallelism documentation** — documents worker scheduling, spool cleanup, cancellation, queue bounds, and multi-source ordering guarantees.
- **`one_to_many` reconciliation pass** — matches one left transaction against N right transactions by summing amounts; includes ambiguous group detection, monetary invariants, and a `GroupedEventWriter` interface for grouped result emission.
- **`group_by` field for `one_to_many` passes** — defaults to `"reference"`, validated against known built-in keys, and wired into `matchByReferenceOneToMany`.
- **Many-to-many reconciliation pass** — supports matching left and right sets with arbitrary cardinality.
- **Configurable duplicate handling policy (`duplicate_policy`)** — four values: `flag` (default), `keep`, `merge`, `latest`; both batch and streaming paths (including multi-source) respect the policy.
- **Bounded-memory partitioned reconciliation backend** — new internal backend that splits large datasets into memory-safe partitions, enabling reconciliation of datasets that exceed available RAM.
- **Partitioned multi-source reconciliation** — extends the partitioned backend to run across multiple counterpart sources in a single pass.
- **Resource-aware index selection** — automatically selects the memory or disk index backend based on available RAM at runtime; Windows resource probing supported.
- **Reconciliation progress telemetry** — structured lifecycle events and progress reporting emitted to stderr during long-running reconciliations.
- **Configurable result emission modes (`--result-mode`)** — `all` (default), `exceptions_only`, and `summary_only` control which events are written to output, reducing noise for large runs.
- **Preflight quality and security gate** — `make check` now runs dependency audit, formatting, lint, security scan, race-tested coverage, build, and smoke benchmarks as a single local gate before opening a PR.
- **LLM-agent-ready CLI** — adds `--error-format json` for machine-readable error output, meaningful exit codes (0 = success, 1 = unmatched, 2 = validation failure, 3 = fatal error), `--fail-if-unmatched` flag, and `reconify config schema` command to emit the full config JSON schema.
- **Agent skill installer** — `npx @reconifyhq/skills` copies the canonical `.agents/skills/` files into any project; new skills added: `reconify-debug`, `reconify-bootstrap`, `reconify-ci`. Expanded `llms.txt` for LLM discoverability.
- **Benchmark suite** — deterministic and realistic benchmarks for the partitioned backend and multi-source reconciliation scenarios.

### Performance

- Partitioned single-source, grouped, and multi-source reconciliation can use bounded parallel workers without sharing the final result writer.
- Disk index inserts are now batched in transactions, reducing write overhead on large right-side files.
- Reduced disk index match write amplification — match entries are written more efficiently during the match pass.
- Bounded grouped partition reconciliation memory — grouped partitions no longer accumulate unbounded row sets.
- Optimized partitioned duplicate pre-scan — reduces redundant work when deduplicating rows across partition boundaries.

### Fixed

- Grouped partition sort failures are now propagated instead of producing an empty successful partition result.
- Partition workers preserve per-partition telemetry reporting for single-source and multi-source runs.
- Preserved partitioned grouped reconciliation semantics — grouping logic is now correctly applied within each partition boundary.
- Hardened resource-aware index selection — stabilized the probing heuristic and fixed a lint warning in the selection path.
- Hardened telemetry output and lifecycle events — telemetry events are flushed and reported correctly even on early cancellation.
- Hardened reconciliation correctness — addressed correctness risks around match scoring and aggregate accounting.
- Propagate cancellation signals and close sort cursors cleanly to avoid resource leaks on early exit.
- Report telemetry lifecycle failures consistently instead of silently swallowing errors.
- Hardened reconciliation diagnostics and aggregate computation for edge cases in large datasets.

### Refactored

- Split engine and CLI responsibilities — engine packages no longer import CLI concerns, making the library surface cleaner.
- Extracted acyclic packages from the engine to remove import cycles and improve testability.

## [0.3.0] - 2026-06-21

### Added

- **Multi-counterpart reconciliation (`rights`)** — A pair can now reconcile one left source against several ordered counterpart sources, carrying unmatched left rows forward between passes.
- **Per-counterpart output summaries** — JSON, JSON stream, and NDJSON outputs now include source-level breakdowns via `by_source` / `source_summary` alongside the aggregate summary.
- **Explicit one-to-one reconciliation passes** — Pairs can define `passes` with `reference_one_to_one` and `name_tokens_one_to_one` to make the matching pipeline explicit.
- **Benchmark infrastructure** — Added deterministic and realistic benchmark generators, runners, validation helpers, and CI benchmark workflow scaffolding.
- **Pull request template** — Added a GitHub PR template and documented the expected PR workflow for agents.

### Changed

- Clarified multi-counterpart docs, examples, and configuration references, including the distinction between ordered counterpart sources (`rights`) and matching strategies (`passes`).
- Improved reconciliation correctness around explicit pass ordering, duplicate annotation, best-candidate selection, summary accounting, and batch/streaming parity.
- Updated docs links to the current Fumadocs content paths.

### Fixed

- `one_to_many` is no longer accepted as a configured pass type. It is not implemented in v0.3.0 and now fails validation instead of silently behaving like a no-op in batch reconciliation.

### Known Limitations

- Multi-counterpart CLI runs do not yet support `--audit`, `--right-file`, or token-mode streaming reconciliation.

## [0.2.0] - 2026-05-17

### Added

- **`reconify config init`** — Interactive wizard (powered by Huh TUI) that reads source file headers, asks you to map transaction fields, and writes a validated `reconify.yaml`. Flags: `--out` (destination path, default `reconify.yaml`) and `--force` (overwrite existing file).
- **Multi-format input parsing** — The parser now supports JSON arrays (`.json`), NDJSON/JSON-L (`.ndjson`), and Excel workbooks (`.xlsx`, `.xlsm`) in addition to CSV. Format is auto-detected from the file extension when `parser_type` is `auto` or unset.
- **Fumadocs documentation site** — New `docs/` site built with Fumadocs and Next.js, covering getting started, configuration reference, engine internals, and performance.

### Fixed

- Trailing content after a JSON array is now rejected with a descriptive parse error instead of being silently ignored.
- Reconcile audit `hashFile` now captures file size and modification time alongside the SHA-256 hash, surfacing metadata divergence in audit output.
- CSV output fields are sanitised against spreadsheet formula injection — values starting with `=`, `+`, `-`, `@`, `\t`, or `\r` are escaped via `SanitizeCSVField`.

### Changed

- `JSONStreamWriter` documentation clarified: it is not O(1) memory. Use `NDJSONWriter` or `CSVWriter` when memory must stay constant with result size.

## [0.1.1] - 2026-03-23

### Fixed

- Corrected Go module path to `github.com/reconifyhq/reconify` across all import references.

## [0.1.0] - 2026-03-23

### Added

#### CLI Commands
- **`reconify reconcile`** — Core reconciliation command that matches transactions between two CSV sources using a 3-tier matching engine (reference exact match, name token similarity via Jaccard scoring, and duplicate detection).
- **`reconify parse`** — Parse and inspect a single CSV file according to source configuration. Useful for debugging parser setup. Supports `ndjson`, `csv`, `table`, and `json` output formats.
- **`reconify config validate`** — Validate configuration file structure and syntax, checking all required fields.
- **`reconify config check-source`** — Validate a CSV file's column structure against a source definition in the config.
- **`reconify validate`** — Standalone data quality validation for source files before reconciliation. Reports record-level, source-level, and cross-source issues with error/warning severity.

#### Reconciliation Engine
- 3-tier matching algorithm: reference exact match, optional name-token similarity (Jaccard, threshold > 0.5), and duplicate detection.
- Configurable date window (`date_window`) and amount tolerance (`amount_tolerance_minor`) per pair.
- Streaming two-pass architecture: build right-side index in pass 1, match left-side in pass 2, with O(1) memory for matching.
- Throughput of ~91k–105k rows/sec on 20M x 20M reconciliations.

#### Index Backends
- **Memory index** — Hash map backed, fastest performance, default backend.
- **Disk index** — SQLite-backed with WAL journaling for lower RAM usage on large datasets.
- **Auto backend** — Switches between memory and disk based on right-file size threshold (`auto_max_right_file_mb`, default 2048 MB).

#### Output Formats
- `json` — Pretty-printed full result object (best for < 500k rows).
- `json-stream` — Line-by-line JSON encoding with lower GC pressure.
- `ndjson` — One tagged JSON line per event, O(1) memory, crash-safe.
- `csv` — Fixed-schema tab-separated output, O(1) memory.
- `table` — Aligned ASCII table for interactive inspection.

#### Data Validation
- Record-level checks: parseable dates and amounts, valid currency format.
- Source-level checks: currency consistency, duplicate references, empty sources, high rate of empty references.
- Cross-source checks: currency compatibility, date range overlap, row count ratio disparity.
- `--fail-on-warning` flag to treat warnings as errors.
- Validation runs by default before reconciliation (skip with `--skip-validation`).

#### Audit & Reproducibility
- `--audit` flag embeds run provenance in output: SHA-256 file hashes, timestamps, tool version, and pair config snapshot.
- `--audit-fixed-timestamp` freezes timestamp and run ID for byte-identical reruns.
- `--deterministic` flag sorts output sections for stable diff-based audit trails.

#### CSV Parser
- Case-insensitive column lookup with trimmed whitespace.
- Configurable date layout, timezone, decimal/thousands separators.
- Parenthetical negative amount support (e.g., `(1,234.56)`).
- Minor-unit multiplier for currency conversion (e.g., dollars to cents).
- 1 MB read buffer with streaming architecture (`ParseCSVEach`).
- Optional `skip_raw` to skip per-row raw map allocation for memory optimization.
- 1000-entry date parse cache for repeated date values.

#### CLI Flags & Configuration
- Global `--config` / `-c` flag with `RECONIFY_CONFIG` env var fallback.
- Global `--verbose` / `-v` for verbose output.
- `--version` flag for version and build time display.
- `--progress` and `--progress-every` for stderr progress logging on large files.
- `--max-token-buffer` for token-mode unmatched buffer row limit.
- `--left-file` / `--right-file` overrides for explicit CSV paths (bypasses glob patterns).

#### Configuration File
- YAML-based config (`reconify.yaml`) with version, timezone, sources, pairs, and index backend settings.
- Source definitions with file glob patterns, parser type, column mappings, and format options.
- Pair definitions with left/right source references, date window, amount tolerance, and name matching mode.

#### Build & Distribution
- Cross-compilation targets: Linux x86_64, macOS x86_64/ARM64, Windows.
- Version and build time injected via linker flags.
- Usable as a Go library via `config` and `engine` packages.

### Internal

- Module path set to `github.com/reconifyhq/reconify`.
- Saturating uint8 counter for duplicate tracking (capped at 2, memory-efficient).
- Bucket struct optimization: no `time.Time` pointer or redundant reference string in index entries.
- Conditional pass-3 file re-scan only when duplicates are detected.
