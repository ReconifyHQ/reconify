#!/usr/bin/env bash
# bench_rss.sh — measure peak RSS and GC behaviour for a 1M-row reconciliation run.
#
# Usage:
#   ./scripts/bench_rss.sh [rows]
#
# Requirements:
#   - reconify binary must be built: go build -o /tmp/reconify-bench ./cmd/reconify
#   - reconify.yaml with a "bench" pair configured, OR this script generates one
#
# The script:
#   1. Generates two synthetic 1M-row CSV files (left and right)
#   2. Runs reconify reconcile with --format=ndjson (streaming, O(1) output)
#   3. Reports peak RSS using platform-appropriate tools
#   4. Enables GODEBUG=gctrace=1 to expose GC churn
#
# Cross-platform:
#   macOS  — uses /usr/bin/time -l (reports "maximum resident set size")
#   Linux  — uses /usr/bin/time -v (reports "Maximum resident set size")

set -euo pipefail

ROWS="${1:-1000000}"
TMPDIR_BENCH="$(mktemp -d)"
trap 'rm -rf "$TMPDIR_BENCH"' EXIT

echo "==> bench_rss: generating ${ROWS} synthetic rows..."

LEFT_CSV="$TMPDIR_BENCH/left.csv"
RIGHT_CSV="$TMPDIR_BENCH/right.csv"
CONFIG_YAML="$TMPDIR_BENCH/reconify.yaml"
BINARY="/tmp/reconify-bench"

# Build binary if needed
if [[ ! -f "$BINARY" ]]; then
  echo "==> Building reconify binary..."
  go build -o "$BINARY" "$(dirname "$0")/../cmd/reconify"
fi

# Generate synthetic CSV files
python3 - <<PYEOF
import csv, random, sys

rows = $ROWS
dates = ["2024-01-{:02d}".format(d) for d in range(1, 29)]

def write_csv(path, rows):
    with open(path, "w", newline="") as f:
        w = csv.writer(f)
        w.writerow(["date", "amount", "currency", "reference", "name"])
        for i in range(rows):
            w.writerow([
                dates[i % len(dates)],
                "{}.{:02d}".format(100 + i % 10000, i % 100),
                "USD",
                "REF{:010d}".format(i),
                "Merchant {}".format(i % 500),
            ])

write_csv("$LEFT_CSV", rows)
write_csv("$RIGHT_CSV", rows)
print(f"Generated {rows} rows each side", file=sys.stderr)
PYEOF

# Write a minimal reconify.yaml for the bench pair
cat > "$CONFIG_YAML" <<YAML
version: 1
timezone: UTC

sources:
  left:
    file_pattern: "$LEFT_CSV"
    parser:
      type: csv
      date_col: date
      date_layout: "2006-01-02"
      amount_col: amount
      multiplier: 100
      currency_col: currency
      ref_col: reference
      name_col: name
      skip_raw: true

  right:
    file_pattern: "$RIGHT_CSV"
    parser:
      type: csv
      date_col: date
      date_layout: "2006-01-02"
      amount_col: amount
      multiplier: 100
      currency_col: currency
      ref_col: reference
      name_col: name
      skip_raw: true

pairs:
  bench:
    left: left
    right: right
    amount_tolerance_minor: 0
YAML

echo "==> Running reconciliation with GODEBUG=gctrace=1 (${ROWS} rows each side)..."
echo ""

GC_LOG="$TMPDIR_BENCH/gctrace.txt"
PLATFORM="$(uname -s)"

if [[ "$PLATFORM" == "Darwin" ]]; then
  # macOS: /usr/bin/time -l reports "maximum resident set size" in bytes
  GODEBUG=gctrace=1 \
    /usr/bin/time -l \
    "$BINARY" reconcile \
      --config "$CONFIG_YAML" \
      --pair bench \
      --format ndjson \
      --out /dev/null \
    2>&1 | tee "$GC_LOG"

  echo ""
  echo "==> Peak RSS (macOS):"
  grep -i "maximum resident" "$GC_LOG" || true

else
  # Linux: /usr/bin/time -v reports "Maximum resident set size (kbytes)"
  GODEBUG=gctrace=1 \
    /usr/bin/time -v \
    "$BINARY" reconcile \
      --config "$CONFIG_YAML" \
      --pair bench \
      --format ndjson \
      --out /dev/null \
    2>&1 | tee "$GC_LOG"

  echo ""
  echo "==> Peak RSS (Linux):"
  grep -i "maximum resident" "$GC_LOG" || true
fi

echo ""
echo "==> GC events (summary):"
grep -c "^gc " "$GC_LOG" 2>/dev/null && \
  echo "GC cycles fired. Review $GC_LOG for full trace." || \
  echo "(no GC trace lines found)"

echo ""
echo "Done. Full output saved to $GC_LOG"
