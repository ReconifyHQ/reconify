#!/usr/bin/env bash
# Adversarial benchmark suite for Reconify.
#
# Generates deterministic adversarial fixtures and runs the full semantic
# parity matrix across supported output formats and index backends.
#
# Usage:
#   ./benchmarks/adversarial/run.sh [options]
#
# Options:
#   --rows N           Left-side row count (default 100000)
#   --output-dir DIR   Benchmark output root (default benchmarks/.out)
#   --cache-mode MODE  warm|cold (default warm)
#   --seed N           deterministic fixture seed passed to the generator (default 42)
#   --binary PATH      Path to pre-built reconify binary
#   --skip-build       Skip binary build (binary must exist)
#   --smoke            Run smoke-only: 500 rows, ndjson+json formats only
#
# Exit code: 0 all parity checks pass; 1 any check fails.
#
# Warm mode pre-reads input files to populate the OS page cache.
# Cold mode only attempts privileged cache eviction locally/manually and
# reports whether it succeeded; cache-state is recorded in report.json.
# Cold cache measurements are best-effort and never a portable CI requirement.

set -euo pipefail

ROWS=100000
OUTPUT_DIR=""
CACHE_MODE="warm"
SEED=42
BINARY=""
SKIP_BUILD=false
SMOKE=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --rows)       ROWS="$2";       shift 2 ;;
    --output-dir) OUTPUT_DIR="$2"; shift 2 ;;
    --cache-mode) CACHE_MODE="$2"; shift 2 ;;
    --seed)       SEED="$2";       shift 2 ;;
    --binary)     BINARY="$2";     shift 2 ;;
    --skip-build) SKIP_BUILD=true; shift ;;
    --smoke)      SMOKE=true;      shift ;;
    *) echo "unknown flag: $1" >&2; exit 1 ;;
  esac
done

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
COMMON_DIR="$(cd "$SCRIPT_DIR/../common" && pwd)"
# shellcheck source=../common/common.sh
source "$COMMON_DIR/common.sh"

REPO_ROOT="$(benchmark_repo_root)"
OUTPUT_DIR="$(resolve_bench_output_dir "$REPO_ROOT" "$OUTPUT_DIR")"
OUTPUT_DIR="$(mkdir -p "$OUTPUT_DIR" && cd "$OUTPUT_DIR" && pwd)"
BINARY="${BINARY:-$OUTPUT_DIR/bin/reconify-bench}"

RUN_ROOT="$OUTPUT_DIR/runs/adversarial"
CONFIG_ROOT="$OUTPUT_DIR/configs/adversarial"
REPORT_FILE="$RUN_ROOT/report.json"
SUMMARY_FILE="$RUN_ROOT/summary.tsv"
mkdir -p "$RUN_ROOT" "$CONFIG_ROOT"

build_bench_binary "$REPO_ROOT" "$BINARY" "$SKIP_BUILD"

if [[ "$SMOKE" == "true" ]]; then
  ROWS=500
fi

PLATFORM="$(uname -s)"
GO_VERSION="$(go version | awk '{print $3}')"
ENGINE_VERSION="$(git -C "$REPO_ROOT" rev-parse --short HEAD 2>/dev/null || echo unknown)"
HOST_ARCH="$(uname -m)"
CPU_COUNT="$(getconf _NPROCESSORS_ONLN 2>/dev/null || echo 0)"
if [[ "$PLATFORM" == "Darwin" ]]; then
  HOST_MEMORY_MB="$(sysctl -n hw.memsize 2>/dev/null | awk '{printf "%d", $1 / 1024 / 1024}')"
else
  HOST_MEMORY_MB="$(awk '/MemTotal:/ {printf "%d", $2 / 1024; exit}' /proc/meminfo 2>/dev/null || echo 0)"
fi

if [[ "$SMOKE" == "true" ]]; then
  TIMEOUT_SECONDS="${BENCH_TIMEOUT_SECONDS:-30}"
else
  TIMEOUT_SECONDS="${BENCH_TIMEOUT_SECONDS:-300}"
fi

manifest_value() {
  local path="$1"
  local key="$2"
  awk -F: -v key="$key" 'index($1, key) {gsub(/[ ,]/, "", $2); gsub(/"/, "", $2); print $2; exit}' "$path"
}

json_number_or_zero() {
  local value="$1"
  if [[ "$value" =~ ^[0-9]+([.][0-9]+)?$ ]]; then
    echo "$value"
  else
    echo 0
  fi
}

# ---------------------------------------------------------------------------
# Cache helpers
# ---------------------------------------------------------------------------

warm_cache() {
  local files=("$@")
  for f in "${files[@]}"; do
    if [[ -f "$f" ]]; then
      # Read file through /dev/null to populate OS page cache.
      if [[ "$PLATFORM" == "Darwin" ]]; then
        cat "$f" > /dev/null
      else
        cat "$f" > /dev/null
      fi
    fi
  done
}

evict_cache() {
  # Best-effort cache eviction. Reports success/failure in cache_state.
  if [[ "$PLATFORM" == "Darwin" ]]; then
    if command -v purge &>/dev/null && sudo -n purge 2>/dev/null; then
      echo "cold_evicted"
    else
      echo "cold_eviction_unavailable"
    fi
  elif [[ -f /proc/sys/vm/drop_caches ]]; then
    if echo 3 | sudo tee /proc/sys/vm/drop_caches > /dev/null 2>&1; then
      echo "cold_evicted"
    else
      echo "cold_eviction_unavailable"
    fi
  else
    echo "cold_eviction_unavailable"
  fi
}

# ---------------------------------------------------------------------------
# Config generation
# ---------------------------------------------------------------------------

write_adversarial_config() {
  local path="$1"
  local pair_name="$2"
  local scenario_dir="$3"
  local scenario="$4"
  local backend="$5"
  local spill_dir="$6"

  {
    cat <<YAML
version: 1
timezone: UTC

index:
  backend: $backend
  spill_dir: "$spill_dir"
YAML

    if [[ "$backend" == "partitioned" ]]; then
      echo "  partition_count: 4"
    fi

    cat <<YAML

sources:
  bank:
    file_pattern: "$scenario_dir/left.csv"
    parser:
      type: csv
      date_col: date
      date_layout: "2006-01-02"
      amount_col: amount
      multiplier: 1
      currency_col: currency
      ref_col: ref_id
      name_col: description
      skip_raw: true
YAML

    case "$scenario" in
      high_duplicate_pressure)
        echo "      group_col: processor_hint"
        ;;
      one_to_many_settlement|many_to_many_settlement)
        echo "      duplicate_policy: keep"
        ;;
    esac

    cat <<YAML

  processor:
    file_pattern: "$scenario_dir/right.csv"
    parser:
      type: csv
      date_col: txn_date
      date_layout: "2006-01-02"
      amount_col: txn_amount
      multiplier: 1
      currency_col: txn_currency
      ref_col: txn_ref
      name_col: merchant
      skip_raw: true
YAML

    case "$scenario" in
      hot_skewed_refs|one_to_many_settlement|many_to_many_settlement)
        echo "      duplicate_policy: keep"
        ;;
    esac

    cat <<YAML

pairs:
  $pair_name:
    left: bank
    right: processor
    date_window: 1d
    amount_tolerance_minor: 0
    name_mode: none
YAML

    case "$scenario" in
      one_to_many_settlement)
        printf "    passes:\n      - type: one_to_many\n"
        ;;
      many_to_many_settlement)
        printf "    passes:\n      - type: many_to_many\n"
        ;;
    esac

  } > "$path"
}

# ---------------------------------------------------------------------------
# Per-run execution and verification
# ---------------------------------------------------------------------------

OVERALL_FAILED=0
REPORT_ENTRIES=()

run_adversarial_check() {
  local scenario="$1"
  local format="$2"
  local backend="$3"
  local scenario_dir="$4"
  local pair_name="$5"

  local run_dir="$RUN_ROOT/$scenario"
  local config="$CONFIG_ROOT/${scenario}_${backend}.yaml"
  local ext="$format"
  [[ "$format" == "json-stream" ]] && ext="json"
  local suffix="$backend""_$format"
  local output_file="$run_dir/output_$suffix.$ext"
  local time_log="$run_dir/time_$suffix.log"
  local spill_dir="$run_dir/spill_$suffix"
  local manifest="$scenario_dir/manifest.json"

  mkdir -p "$run_dir" "$spill_dir"
  write_adversarial_config "$config" "$pair_name" "$scenario_dir" "$scenario" "$backend" "$spill_dir"

  local cache_state="$CACHE_MODE"
  if [[ "$CACHE_MODE" == "warm" ]]; then
    warm_cache "$scenario_dir/left.csv" "$scenario_dir/right.csv"
  elif [[ "$CACHE_MODE" == "cold" ]]; then
    cache_state="$(evict_cache)"
  fi

  local runner_output runner_rc
  runner_output="$(go run "$REPO_ROOT/benchmarks/common/timed_run.go" \
      --timeout-seconds "$TIMEOUT_SECONDS" \
      --stdout "$output_file" \
      --stderr "$time_log" \
      --monitor-dir "$spill_dir" \
      -- \
      "$BINARY" reconcile \
        --config "$config" \
        --pair "$pair_name" \
        --format "$format")" && runner_rc=0 || runner_rc=$?

  local metrics_line elapsed_seconds rss_mb peak_temp_bytes timed_out child_exit
  metrics_line="$(printf '%s\n' "$runner_output" | tail -1)"
  if [[ "$metrics_line" == TIMED_RUN$'\t'* ]]; then
    IFS=$'\t' read -r _ elapsed_seconds rss_mb peak_temp_bytes timed_out child_exit <<< "$metrics_line"
  else
    elapsed_seconds=0
    rss_mb=0
    peak_temp_bytes=0
    timed_out=0
    child_exit="$runner_rc"
  fi
  elapsed_seconds="$(json_number_or_zero "$elapsed_seconds")"
  rss_mb="$(json_number_or_zero "$rss_mb")"
  peak_temp_bytes="$(json_number_or_zero "$peak_temp_bytes")"
  local elapsed temp_disk_mb gc_count output_bytes
  elapsed="$(awk -v seconds="$elapsed_seconds" 'BEGIN {printf "%.2fs", seconds}')"
  temp_disk_mb="$(awk -v bytes="$peak_temp_bytes" 'BEGIN {printf "%.1f", bytes / 1024 / 1024}')"
  gc_count="$(gc_count "$time_log")"
  output_bytes=0
  if [[ -f "$output_file" ]]; then
    output_bytes="$(wc -c < "$output_file" | tr -d ' ')"
  fi
  local total_left total_right input_rows rows_per_sec
  total_left="$(manifest_value "$manifest" total_left)"
  total_right="$(manifest_value "$manifest" total_right)"
  total_left="$(json_number_or_zero "$total_left")"
  total_right="$(json_number_or_zero "$total_right")"
  input_rows=$((total_left + total_right))
  rows_per_sec="$(awk -v rows="$input_rows" -v seconds="$elapsed_seconds" 'BEGIN {if (seconds > 0) printf "%.1f", rows / seconds; else print 0}')"

  local verify_out verify_exit verify_status warning_json
  verify_out=""
  warning_json="null"
  if [[ "$timed_out" == "1" ]]; then
    verify_status="timeout"
    warning_json="\"run exceeded $TIMEOUT_SECONDS s timeout\""
    echo "  TIMEOUT $scenario format=$format backend=$backend"
    if [[ "$SMOKE" == "true" ]]; then
      OVERALL_FAILED=1
    fi
  elif [[ "$runner_rc" -ne 0 || "$child_exit" -ne 0 ]]; then
    verify_status="fail"
    warning_json="\"reconcile exited with status $child_exit\""
    echo "  ERROR: reconcile failed for $scenario format=$format backend=$backend"
    OVERALL_FAILED=1
  else
    verify_out="$(go run "$REPO_ROOT/benchmarks/common/verify_adversarial.go" \
        --manifest "$manifest" \
        --actual "$output_file" \
        --format "$format" \
        --backend "$backend" 2>&1)" && verify_exit=0 || verify_exit=$?
    case "$verify_exit" in
      0)  verify_status="pass" ;;
      2)  verify_status="skip" ;;
      *)  verify_status="fail"; OVERALL_FAILED=1 ;;
    esac
    echo "  $verify_out"
  fi

  printf "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n" \
   "$scenario" "$format" "$backend" "$cache_state" \
    "$elapsed" "$elapsed_seconds" "$rows_per_sec" "$rss_mb" "$temp_disk_mb" \
    "$gc_count" "$output_bytes" "$verify_status" \
   >> "$SUMMARY_FILE"

  local entry
  entry="$(printf '{
    "scenario": "%s",
    "format": "%s",
   "backend": "%s",
   "cache_state": "%s",
    "input_rows": %d,
    "input_rows_per_sec": %s,
    "elapsed": "%s",
    "elapsed_seconds": %s,
    "peak_rss_mb": %s,
    "peak_temp_disk_mb": %s,
    "gc_count": %s,
    "alloc_bytes": null,
    "output_bytes": %s,
    "parity_status": "%s",
    "warning": %s
 }' "$scenario" "$format" "$backend" "$cache_state" \
       "$input_rows" "$rows_per_sec" "$elapsed" "$elapsed_seconds" \
       "$rss_mb" "$temp_disk_mb" "$gc_count" "$output_bytes" \
       "$verify_status" "$warning_json")"
  REPORT_ENTRIES+=("$entry")
}

# ---------------------------------------------------------------------------
# Scenario list
# ---------------------------------------------------------------------------

ALL_SCENARIOS=(
  uniform_unique_refs
  hot_skewed_refs
  high_duplicate_pressure
  high_result_emission
  one_to_many_settlement
  many_to_many_settlement
)

# Reference one-to-one scenarios support all formats and all streaming backends.
REF_ONE_TO_ONE_SCENARIOS=(
  uniform_unique_refs
  hot_skewed_refs
  high_duplicate_pressure
  high_result_emission
)

# Grouped scenarios: only ndjson/json/json-stream formats; memory backend only.
GROUPED_SCENARIOS=(
  one_to_many_settlement
  many_to_many_settlement
)

if [[ "$SMOKE" == "true" ]]; then
  FORMATS_REF=("ndjson" "json")
  BACKENDS_REF=("memory" "disk" "partitioned")
  FORMATS_GROUPED=("ndjson" "json")
  BACKENDS_GROUPED=("memory")
else
  FORMATS_REF=("ndjson" "json" "json-stream" "csv" "table")
  BACKENDS_REF=("memory" "disk" "partitioned")
  FORMATS_GROUPED=("ndjson" "json" "json-stream")
  BACKENDS_GROUPED=("memory")
fi

rm -f "$SUMMARY_FILE"
printf "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n" \
  "scenario" "format" "backend" "cache" "wall" "seconds" "rows_per_sec" "rss_mb" "temp_disk_mb" "gc" "bytes" "status" \
  > "$SUMMARY_FILE"

echo "==> adversarial benchmarks"
echo "    rows:       $ROWS"
echo "    seed:       $SEED"
echo "    cache-mode: $CACHE_MODE"
echo "    output dir: $OUTPUT_DIR"
echo "    binary:     $BINARY"
echo ""

# ---------------------------------------------------------------------------
# Generate fixtures and run parity matrix
# ---------------------------------------------------------------------------

for scenario in "${ALL_SCENARIOS[@]}"; do
  scenario_dir="$RUN_ROOT/$scenario"
  mkdir -p "$scenario_dir"

  echo "==> generating: $scenario"
  go run "$REPO_ROOT/benchmarks/generators/adversarial.go" \
    --scenario "$scenario" \
    --rows "$ROWS" \
    --output-dir "$scenario_dir" \
    --seed "$SEED"

  pair_name="bench_${scenario}"

  is_grouped=false
  for gs in "${GROUPED_SCENARIOS[@]}"; do
    [[ "$gs" == "$scenario" ]] && is_grouped=true && break
  done

  if [[ "$is_grouped" == "true" ]]; then
    for fmt in "${FORMATS_GROUPED[@]}"; do
      for backend in "${BACKENDS_GROUPED[@]}"; do
        echo "  format=$fmt backend=$backend"
        run_adversarial_check "$scenario" "$fmt" "$backend" "$scenario_dir" "$pair_name"
      done
    done
  else
    for fmt in "${FORMATS_REF[@]}"; do
      for backend in "${BACKENDS_REF[@]}"; do
        echo "  format=$fmt backend=$backend"
        run_adversarial_check "$scenario" "$fmt" "$backend" "$scenario_dir" "$pair_name"
      done
    done
  fi
  echo ""
done

# ---------------------------------------------------------------------------
# Emit report.json
# ---------------------------------------------------------------------------

{
 printf '{\n'
  printf '  "schema_version": 1,\n'
 printf '  "rows": %d,\n' "$ROWS"
 printf '  "seed": %d,\n' "$SEED"
 printf '  "cache_mode": "%s",\n' "$CACHE_MODE"
  printf '  "engine_version": "%s",\n' "$ENGINE_VERSION"
 printf '  "go_version": "%s",\n' "$GO_VERSION"
 printf '  "platform": "%s",\n' "$PLATFORM"
  printf '  "host": {"arch": "%s", "cpu_count": %s, "memory_mb": %s},\n' "$HOST_ARCH" "$CPU_COUNT" "$HOST_MEMORY_MB"
  printf '  "runs": [\n'
  local_sep=""
  for entry in "${REPORT_ENTRIES[@]}"; do
    printf '%s    %s\n' "$local_sep" "$entry"
    local_sep=","
  done
  printf '  ]\n'
  printf '}\n'
} > "$REPORT_FILE"

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------
echo "Summary: $SUMMARY_FILE"
column -t -s $'\t' "$SUMMARY_FILE" 2>/dev/null || cat "$SUMMARY_FILE"
echo ""
echo "Report:  $REPORT_FILE"

if [[ "$OVERALL_FAILED" -ne 0 ]]; then
  echo "FAILED: one or more parity checks failed." >&2
  exit 1
fi
echo "All adversarial parity checks passed."
