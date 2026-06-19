#!/usr/bin/env bash

benchmark_repo_root() {
  local source_dir
  source_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  cd "$source_dir/../.." && pwd
}

resolve_bench_output_dir() {
  local repo_root="$1"
  local explicit="${2:-}"

  if [[ -n "$explicit" ]]; then
    printf '%s\n' "$explicit"
    return
  fi
  if [[ -n "${BENCH_OUTPUT_DIR:-}" ]]; then
    printf '%s\n' "$BENCH_OUTPUT_DIR"
    return
  fi
  if [[ -n "${GITHUB_ACTIONS:-}" && -n "${RUNNER_TEMP:-}" ]]; then
    printf '%s/reconify-benchmarks\n' "$RUNNER_TEMP"
    return
  fi
  printf '%s/benchmarks/.out\n' "$repo_root"
}

build_bench_binary() {
  local repo_root="$1"
  local binary="$2"
  local skip_build="$3"

  if [[ "$skip_build" == "true" ]]; then
    if [[ ! -x "$binary" ]]; then
      echo "ERROR: --skip-build set but binary is not executable: $binary" >&2
      return 1
    fi
    return
  fi
  if [[ -d "$binary" ]]; then
    echo "ERROR: binary path points to a directory: $binary" >&2
    return 1
  fi
  mkdir -p "$(dirname "$binary")"
  go build -o "$binary" "$repo_root/cmd/reconify"
}

run_reconcile_timed() {
  local binary="$1"
  local config="$2"
  local pair="$3"
  local ndjson="$4"
  local log="$5"
  local gctrace="$6"
  local platform
  platform="$(uname -s)"

  mkdir -p "$(dirname "$ndjson")" "$(dirname "$log")"
  if [[ "$platform" == "Darwin" ]]; then
    if [[ "$gctrace" == "true" ]]; then
      GODEBUG=gctrace=1 /usr/bin/time -l \
        "$binary" reconcile --config "$config" --pair "$pair" --format ndjson \
        > "$ndjson" 2> "$log"
    else
      /usr/bin/time -l \
        "$binary" reconcile --config "$config" --pair "$pair" --format ndjson \
        > "$ndjson" 2> "$log"
    fi
  else
    if [[ "$gctrace" == "true" ]]; then
      GODEBUG=gctrace=1 /usr/bin/time -v \
        "$binary" reconcile --config "$config" --pair "$pair" --format ndjson \
        > "$ndjson" 2> "$log"
    else
      /usr/bin/time -v \
        "$binary" reconcile --config "$config" --pair "$pair" --format ndjson \
        > "$ndjson" 2> "$log"
    fi
  fi
}

parse_elapsed() {
  local log="$1"
  if [[ "$(uname -s)" == "Darwin" ]]; then
    awk '/ real / {print $1 "s"; exit}' "$log"
  else
    awk -F': ' '/Elapsed \(wall clock\) time/ {print $2; exit}' "$log"
  fi
}

parse_rss_mb() {
  local log="$1"
  if [[ "$(uname -s)" == "Darwin" ]]; then
    awk '/maximum resident set size/ {printf "%.1f", $1 / 1024 / 1024; exit}' "$log"
  else
    awk -F': ' '/Maximum resident set size/ {printf "%.1f", $2 / 1024; exit}' "$log"
  fi
}

gc_count() {
  local log="$1"
  grep -c '^gc ' "$log" 2>/dev/null || true
}
