#!/usr/bin/env bash
# Deterministic reconciliation benchmark suite.

set -euo pipefail

ROWS=100000
OUTPUT_DIR=""
BINARY=""
GCTRACE=false
SKIP_BUILD=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --rows) ROWS="$2"; shift 2 ;;
    --output-dir) OUTPUT_DIR="$2"; shift 2 ;;
    --binary) BINARY="$2"; shift 2 ;;
    --gctrace) GCTRACE=true; shift ;;
    --skip-build) SKIP_BUILD=true; shift ;;
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

RUN_ROOT="$OUTPUT_DIR/runs/deterministic"
CONFIG_ROOT="$OUTPUT_DIR/configs/deterministic"
SUMMARY_FILE="$RUN_ROOT/summary.tsv"
mkdir -p "$RUN_ROOT" "$CONFIG_ROOT"

build_bench_binary "$REPO_ROOT" "$BINARY" "$SKIP_BUILD"

pct() {
  local value="$1"
  local percent="$2"
  echo $((value * percent / 100))
}

write_config() {
  local path="$1"
  local pair="$2"
  local scenario_dir="$3"
  local sources="$4"
  local group_col="${5:-}"

  {
    cat <<YAML
version: 1
timezone: UTC

index:
  backend: memory

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
    if [[ -n "$group_col" ]]; then
      echo "      group_col: $group_col"
    fi

    if [[ "$sources" -eq 1 ]]; then
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

pairs:
  $pair:
    left: bank
    right: processor
    date_window: 1d
    amount_tolerance_minor: 0
    name_mode: none
YAML
    else
      local i
      for ((i = 1; i <= sources; i++)); do
        cat <<YAML

  processor_$i:
    file_pattern: "$scenario_dir/right_$i.csv"
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
      done

      cat <<YAML

pairs:
  $pair:
    left: bank
    rights:
YAML
      for ((i = 1; i <= sources; i++)); do
        echo "      - processor_$i"
      done
      cat <<YAML
    date_window: 1d
    amount_tolerance_minor: 0
    name_mode: none
YAML
    fi
  } > "$path"
}

write_expected() {
  local path="$1"
  local scenario="$2"
  local total_left="$3"
  local total_right="$4"
  local matched="$5"
  local amount_diff="$6"
  local timing_diff="$7"
  local unmatched_left="$8"
  local unmatched_right="$9"
  local duplicate_count="${10}"
  local duplicate_groups="${11}"

  cat > "$path" <<JSON
{
  "scenario": "$scenario",
  "summary": {
    "total_left": $total_left,
    "total_right": $total_right,
    "matched": $matched,
    "unmatched_left": $unmatched_left,
    "unmatched_right": $unmatched_right,
    "amount_diff_count": $amount_diff,
    "timing_diff_count": $timing_diff,
    "duplicate_count": $duplicate_count
  },
  "duplicate_groups": $duplicate_groups
}
JSON
}

run_scenario() {
  local id="$1"
  local name="$2"
  local pair="$3"
  local sources="$4"
  local group_col="$5"
  local match_pct="$6"
  local amount_pct="$7"
  local timing_pct="$8"
  local right_only_mode="$9"
  local duplicate_groups="${10}"
  shift 10

  local scenario_dir="$RUN_ROOT/scenario${id}"
  local config="$CONFIG_ROOT/scenario${id}.yaml"
  local expected="$scenario_dir/expected.json"
  local ndjson="$scenario_dir/output.ndjson"
  local log="$scenario_dir/time.log"
  rm -rf "$scenario_dir"
  mkdir -p "$scenario_dir"

  echo "==> deterministic scenario $id: $name"
  go run "$REPO_ROOT/benchmarks/generators/deterministic.go" \
    -rows "$ROWS" \
    -out "$scenario_dir" \
    -sources "$sources" \
    -with-error-cases=false \
    "$@"

  write_config "$config" "$pair" "$scenario_dir" "$sources" "$group_col"

  local matched amount_diff timing_diff unmatched_left matchable right_only total_right duplicate_count
  matched="$(pct "$ROWS" "$match_pct")"
  amount_diff="$(pct "$ROWS" "$amount_pct")"
  timing_diff="$(pct "$ROWS" "$timing_pct")"
  unmatched_left=$((ROWS - matched - amount_diff - timing_diff))
  matchable=$((matched + amount_diff + timing_diff))
  if [[ "$right_only_mode" == "double-matchable" ]]; then
    right_only=$((matchable * 2))
  else
    right_only="$(pct "$ROWS" 5)"
  fi
  total_right=$((matchable + right_only))
  duplicate_count=$((duplicate_groups * 2))
  write_expected "$expected" "$name" "$ROWS" "$total_right" "$matched" "$amount_diff" "$timing_diff" "$unmatched_left" "$right_only" "$duplicate_count" "$duplicate_groups"

  run_reconcile_timed "$BINARY" "$config" "$pair" "$ndjson" "$log" "$GCTRACE"
  go run "$REPO_ROOT/benchmarks/common/validate_summary.go" --expected "$expected" --actual "$ndjson"

  local elapsed rss gcs
  elapsed="$(parse_elapsed "$log")"
  rss="$(parse_rss_mb "$log")"
  gcs="$(gc_count "$log")"
  printf "%s\t%s\t%s\t%s\t%s\t%s\t%s\n" "$id" "$name" "$sources" "$elapsed" "$rss" "$gcs" "$scenario_dir" >> "$SUMMARY_FILE"
}

rm -f "$SUMMARY_FILE"
printf "%s\t%s\t%s\t%s\t%s\t%s\t%s\n" "id" "scenario" "rights" "wall" "rss_mb" "gc" "path" > "$SUMMARY_FILE"

echo "==> deterministic benchmarks"
echo "    rows:       $ROWS"
echo "    output dir: $OUTPUT_DIR"
echo "    binary:     $BINARY"

run_scenario 1 "baseline_1_source" "bench_baseline" 1 "" 85 5 5 "five-percent" 0
run_scenario 2 "even_split_1_3" "bench_even_3" 3 "" 85 5 5 "five-percent" 0
run_scenario 3 "skewed_1_3" "bench_skewed_3" 3 "" 85 5 5 "five-percent" 0 -source-weights 70,20,5
run_scenario 4 "high_unmatched_buffer" "bench_high_unmatched" 3 "" 80 5 5 "five-percent" 0 -match-pct 80 -amount-diff-pct 5 -timing-diff-pct 5 -source-weights 10,40,40
run_scenario 5 "right_only_heavy" "bench_right_only_heavy" 3 "" 85 5 5 "double-matchable" 0 -right-only-match-ratio 2
run_scenario 6 "duplicates_spanning_passes" "bench_dups_spanning" 2 "processor_hint" 85 5 5 "five-percent" 100 -duplicate-groups 100 -unique-group-values

echo ""
echo "Summary: $SUMMARY_FILE"
column -t -s $'\t' "$SUMMARY_FILE" 2>/dev/null || cat "$SUMMARY_FILE"
