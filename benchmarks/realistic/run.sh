#!/usr/bin/env bash
# Source-informed synthetic reconciliation benchmark suite.

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

RUN_ROOT="$OUTPUT_DIR/runs/realistic"
CONFIG_ROOT="$OUTPUT_DIR/configs/realistic"
SUMMARY_FILE="$RUN_ROOT/summary.tsv"
mkdir -p "$RUN_ROOT" "$CONFIG_ROOT"

build_bench_binary "$REPO_ROOT" "$BINARY" "$SKIP_BUILD"

scenario_sources() {
  case "$1" in
    payout_reconciliation|bank_statement_noise|multi_currency_settlement) echo 1 ;;
    split_provider_exports|high_noise_right_side) echo 3 ;;
    *) echo 2 ;;
  esac
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
  ledger:
    file_pattern: "$scenario_dir/ledger.csv"
    parser:
      type: csv
      date_col: booking_date
      date_layout: "2006-01-02"
      amount_col: settlement_amount_minor
      multiplier: 1
      currency_col: currency
      ref_col: recon_ref
      name_col: description
      skip_raw: true
YAML
    if [[ -n "$group_col" ]]; then
      echo "      group_col: $group_col"
    fi

    if [[ "$sources" -eq 1 ]]; then
      cat <<YAML

  provider:
    file_pattern: "$scenario_dir/provider.csv"
    parser:
      type: csv
      date_col: report_date
      date_layout: "2006-01-02"
      amount_col: report_amount_minor
      multiplier: 1
      currency_col: report_currency
      ref_col: report_ref
      name_col: descriptor
      skip_raw: true

pairs:
  $pair:
    left: ledger
    right: provider
    date_window: 1d
    amount_tolerance_minor: 0
    name_mode: none
YAML
    else
      local i
      for ((i = 1; i <= sources; i++)); do
        cat <<YAML

  provider_$i:
    file_pattern: "$scenario_dir/provider_$i.csv"
    parser:
      type: csv
      date_col: report_date
      date_layout: "2006-01-02"
      amount_col: report_amount_minor
      multiplier: 1
      currency_col: report_currency
      ref_col: report_ref
      name_col: descriptor
      skip_raw: true
YAML
      done

      cat <<YAML

pairs:
  $pair:
    left: ledger
    rights:
YAML
      for ((i = 1; i <= sources; i++)); do
        echo "      - provider_$i"
      done
      cat <<YAML
    date_window: 1d
    amount_tolerance_minor: 0
    name_mode: none
YAML
    fi
  } > "$path"
}

run_scenario() {
  local scenario="$1"
  local sources
  sources="$(scenario_sources "$scenario")"
  local pair="bench_${scenario}"
  local scenario_dir="$RUN_ROOT/$scenario"
  local config="$CONFIG_ROOT/$scenario.yaml"
  local expected="$scenario_dir/expected.json"
  local ndjson="$scenario_dir/output.ndjson"
  local log="$scenario_dir/time.log"
  local group_col=""
  if [[ "$scenario" == "duplicate_replay_window" ]]; then
    group_col="group_key"
  fi

  rm -rf "$scenario_dir"
  mkdir -p "$scenario_dir"

  echo "==> realistic scenario: $scenario"
  go run "$REPO_ROOT/benchmarks/generators/realistic.go" \
    -rows "$ROWS" \
    -out "$scenario_dir" \
    -scenario "$scenario"

  write_config "$config" "$pair" "$scenario_dir" "$sources" "$group_col"
  run_reconcile_timed "$BINARY" "$config" "$pair" "$ndjson" "$log" "$GCTRACE"
  go run "$REPO_ROOT/benchmarks/common/validate_summary.go" --expected "$expected" --actual "$ndjson"

  local elapsed rss gcs
  elapsed="$(parse_elapsed "$log")"
  rss="$(parse_rss_mb "$log")"
  gcs="$(gc_count "$log")"
  printf "%s\t%s\t%s\t%s\t%s\t%s\n" "$scenario" "$sources" "$elapsed" "$rss" "$gcs" "$scenario_dir" >> "$SUMMARY_FILE"
}

SCENARIOS=(
  payout_reconciliation
  bank_statement_noise
  refunds_disputes_chargebacks
  multi_currency_settlement
  split_provider_exports
  high_noise_right_side
  duplicate_replay_window
  late_settlement_window
)

rm -f "$SUMMARY_FILE"
printf "%s\t%s\t%s\t%s\t%s\t%s\n" "scenario" "rights" "wall" "rss_mb" "gc" "path" > "$SUMMARY_FILE"

echo "==> realistic benchmarks"
echo "    rows:       $ROWS"
echo "    output dir: $OUTPUT_DIR"
echo "    binary:     $BINARY"

for scenario in "${SCENARIOS[@]}"; do
  run_scenario "$scenario"
done

echo ""
echo "Summary: $SUMMARY_FILE"
column -t -s $'\t' "$SUMMARY_FILE" 2>/dev/null || cat "$SUMMARY_FILE"
