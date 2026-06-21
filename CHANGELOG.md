# Changelog

All notable changes to Reconify will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

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
