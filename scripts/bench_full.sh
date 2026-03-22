#!/usr/bin/env bash
# bench_full.sh — production benchmark suite for the reconify streaming engine.
#
# Usage:
#   ./scripts/bench_full.sh [flags]
#
# Flags:
#   --rows N         Rows per file for data generation (default: 20000000)
#   --data-dir DIR   Pre-generated data directory (default: /tmp/bench-data)
#   --cold           Attempt OS page cache drop before RSS run
#                    macOS: requires `sudo purge`
#                    Linux: requires write access to /proc/sys/vm/drop_caches
#   --skip-gen       Skip data generation even if files are absent (fail fast)
#   --rss-only       Skip Go test benchmarks; only measure peak RSS + GC
#
# What this script measures:
#
#   1. Warm-cache throughput  (go test -bench, O(n) allocations measured)
#      Runs BenchmarkParseCSVEach and BenchmarkReconcileStreaming against the
#      large pre-generated files. Files will be in the OS page cache from the
#      previous read, so this measures CPU-bound parse + hash throughput.
#
#   2. Peak RSS + GC churn  (single binary run with /usr/bin/time + GODEBUG)
#      Runs `reconify reconcile` on 20M×20M rows with --format=ndjson (O(1)
#      output). Captures peak RSS and GC cycle count.
#      If --cold is passed, the page cache is dropped first so the first pass
#      through both CSV files causes cold page faults, reflecting true I/O cost.
#
# Expected results (Apple M1 Pro / NVMe, 20M rows):
#   Parse throughput:        ~1.5–2.0 M rows/sec (warm cache)
#   Reconcile throughput:    ~250–400 k rows/sec (warm cache)
#   Peak RSS (right index):  ~10–12 GB (right side held in memoryIndex)
#   GC cycles:               <20 for ndjson output format
#
# Requirements:
#   - Go ≥ 1.24 on PATH
#   - No Python required; data generator is pure Go

set -euo pipefail

ROWS=20000000
DATA_DIR="/tmp/bench-data"
COLD_CACHE=false
SKIP_GEN=false
RSS_ONLY=false
PROGRESS=true
PROGRESS_EVERY=1000000
BINARY="/tmp/reconify-bench-full"

# ── Parse flags ───────────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case "$1" in
    --rows)      ROWS="$2";     shift 2 ;;
    --data-dir)  DATA_DIR="$2"; shift 2 ;;
    --cold)      COLD_CACHE=true; shift ;;
    --skip-gen)  SKIP_GEN=true; shift ;;
    --rss-only)  RSS_ONLY=true; shift ;;
    --no-progress) PROGRESS=false; shift ;;
    --progress-every) PROGRESS_EVERY="$2"; shift 2 ;;
    *) echo "unknown flag: $1"; exit 1 ;;
  esac
done

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
PLATFORM="$(uname -s)"
LEFT_CSV="${DATA_DIR}/left.csv"
RIGHT_CSV="${DATA_DIR}/right.csv"
CONFIG_YAML="${DATA_DIR}/reconify.yaml"
GC_LOG="${DATA_DIR}/gctrace.txt"

header() {
  echo ""
  echo "════════════════════════════════════════════════════════════"
  echo " $*"
  echo "════════════════════════════════════════════════════════════"
}

header "reconify bench_full  rows=${ROWS}  platform=${PLATFORM}"
echo "  data dir:  ${DATA_DIR}"
echo "  cold:      ${COLD_CACHE}"
echo "  rss-only:  ${RSS_ONLY}"
echo "  progress:  ${PROGRESS} (every ${PROGRESS_EVERY} rows)"
echo ""

# ── Step 1: Generate benchmark data ──────────────────────────────────────────
if [[ -f "$LEFT_CSV" && -f "$RIGHT_CSV" ]]; then
  L_SIZE=$(du -sh "$LEFT_CSV"  2>/dev/null | cut -f1 || echo "?")
  R_SIZE=$(du -sh "$RIGHT_CSV" 2>/dev/null | cut -f1 || echo "?")
  echo "==> [1/4] Using existing data files"
  echo "    left.csv:  ${L_SIZE}   ${LEFT_CSV}"
  echo "    right.csv: ${R_SIZE}   ${RIGHT_CSV}"
  echo "    (delete files and re-run to regenerate)"
elif [[ "$SKIP_GEN" == "true" ]]; then
  echo "ERROR: --skip-gen set but ${DATA_DIR}/{left,right}.csv not found." >&2
  echo "       Run: go run scripts/gen_bench_data.go --rows ${ROWS} --out ${DATA_DIR}" >&2
  exit 1
else
  echo "==> [1/4] Generating ${ROWS} rows per side..."
  go run "${SCRIPT_DIR}/gen_bench_data.go" --rows "$ROWS" --out "$DATA_DIR"
fi

# Normalize benchmark data dir to absolute path so BENCH_DATA_DIR resolves
# correctly when `go test` runs package benchmarks from ./engine.
DATA_DIR="$(cd "$DATA_DIR" && pwd)"
LEFT_CSV="${DATA_DIR}/left.csv"
RIGHT_CSV="${DATA_DIR}/right.csv"
echo ""

# ── Step 2: Build binary ──────────────────────────────────────────────────────
echo "==> [2/4] Building reconify binary..."
go build -o "$BINARY" "${REPO_ROOT}/cmd/reconify"
echo "    ${BINARY}"
echo ""

# ── Step 3: Go test benchmarks (warm cache) ───────────────────────────────────
if [[ "$RSS_ONLY" == "false" ]]; then
  echo "==> [3/4] Go benchmarks (warm OS page cache)"
  echo ""
  echo "    ── ParseCSVEach (synthetic, in-process files) ──"
  go test \
    -run='^$' \
    -bench='BenchmarkParseCSVEach' \
    -benchmem \
    -benchtime=3s \
    -count=1 \
    "${REPO_ROOT}/engine/..." 2>&1

  echo ""
  echo "    ── ReconcileStreaming (synthetic 100k/1M) ──"
  go test \
    -run='^$' \
    -bench='BenchmarkReconcileStreaming_100k|BenchmarkReconcileStreaming_1M$' \
    -benchmem \
    -benchtime=1x \
    -timeout=300s \
    -count=1 \
    "${REPO_ROOT}/engine/..." 2>&1

  echo ""
  echo "    ── ReconcileStreaming_20M (realistic mixed, large-file) ──"
  echo "    Note: runs -benchtime=1x (single iteration) due to file size."
  echo "    Expected: ~60–120 s depending on storage speed."
  BENCH_DATA_DIR="$DATA_DIR" \
  go test \
    -run='^$' \
    -bench='Benchmark(ParseCSVEach_20M_Left|ReconcileStreaming_20M_Realistic)$' \
    -benchmem \
    -benchtime=1x \
    -timeout=600s \
    -count=1 \
    "${REPO_ROOT}/engine/..." 2>&1

  echo ""
else
  echo "==> [3/4] Skipped (--rss-only)"
  echo ""
fi

# ── Step 4: Peak RSS + GC trace ───────────────────────────────────────────────

# Write reconify.yaml for the bench pair.
# Uses different column names for left (bank) vs right (payment processor)
# to match the generated CSV schemas.
cat > "$CONFIG_YAML" <<YAML
version: 1
timezone: UTC

sources:
  bank:
    file_pattern: "${LEFT_CSV}"
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

  processor:
    file_pattern: "${RIGHT_CSV}"
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
  bench:
    left: bank
    right: processor
    amount_tolerance_minor: 0
YAML

# Optionally drop OS page cache for cold-I/O measurement.
if [[ "$COLD_CACHE" == "true" ]]; then
  echo "==> Dropping OS page cache (--cold)..."
  if [[ "$PLATFORM" == "Darwin" ]]; then
    if command -v purge &>/dev/null; then
      sudo purge 2>/dev/null && echo "    page cache cleared via purge" \
        || echo "    purge failed (sudo required); continuing with warm cache"
    else
      echo "    'purge' not found; continuing with warm cache"
    fi
  elif [[ "$PLATFORM" == "Linux" ]]; then
    if [[ -w /proc/sys/vm/drop_caches ]]; then
      sync
      echo 3 | sudo tee /proc/sys/vm/drop_caches >/dev/null
      echo "    page cache dropped"
    else
      echo "    /proc/sys/vm/drop_caches not writable; continuing with warm cache"
    fi
  fi
  echo ""
fi

echo "==> [4/4] Peak RSS + GC trace  (${ROWS} rows × 2 sides)"
echo "    format: ndjson (O(1) output)  GODEBUG=gctrace=1"
echo ""

if [[ "$PLATFORM" == "Darwin" ]]; then
  # macOS: /usr/bin/time -l reports "maximum resident set size" in bytes
  if [[ "$PROGRESS" == "true" ]]; then
    GODEBUG=gctrace=1 \
      /usr/bin/time -l \
      "$BINARY" reconcile \
        --config  "$CONFIG_YAML" \
        --pair    bench \
        --format  ndjson \
        --out     /dev/null \
        --progress \
        --progress-every "$PROGRESS_EVERY" \
      2>&1 | tee "$GC_LOG"
  else
    GODEBUG=gctrace=1 \
      /usr/bin/time -l \
      "$BINARY" reconcile \
        --config  "$CONFIG_YAML" \
        --pair    bench \
        --format  ndjson \
        --out     /dev/null \
      2>&1 | tee "$GC_LOG"
  fi

  echo ""
  echo "==> Peak RSS (macOS):"
  grep -i "maximum resident" "$GC_LOG" | tail -1 || echo "  (not found)"

else
  # Linux: /usr/bin/time -v reports "Maximum resident set size (kbytes)"
  if [[ "$PROGRESS" == "true" ]]; then
    GODEBUG=gctrace=1 \
      /usr/bin/time -v \
      "$BINARY" reconcile \
        --config  "$CONFIG_YAML" \
        --pair    bench \
        --format  ndjson \
        --out     /dev/null \
        --progress \
        --progress-every "$PROGRESS_EVERY" \
      2>&1 | tee "$GC_LOG"
  else
    GODEBUG=gctrace=1 \
      /usr/bin/time -v \
      "$BINARY" reconcile \
        --config  "$CONFIG_YAML" \
        --pair    bench \
        --format  ndjson \
        --out     /dev/null \
      2>&1 | tee "$GC_LOG"
  fi

  echo ""
  echo "==> Peak RSS (Linux):"
  grep -i "maximum resident" "$GC_LOG" | tail -1 || echo "  (not found)"
fi

echo ""
echo "==> GC summary:"
GC_COUNT=$(grep -c "^gc " "$GC_LOG" 2>/dev/null || true)
GC_COUNT="${GC_COUNT:-0}"
echo "    GC cycles fired: ${GC_COUNT}"
if [[ "$GC_COUNT" -gt 50 ]]; then
  echo "    WARNING: high GC pressure. Consider increasing GOGC or using a disk-backed index."
fi

echo ""
echo "    Full output saved to: ${GC_LOG}"
echo ""
header "Benchmark complete"
