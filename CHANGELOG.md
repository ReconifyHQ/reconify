# Changelog

All notable changes to Reconify will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

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
