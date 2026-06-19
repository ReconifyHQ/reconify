#!/usr/bin/env bash
# Compatibility wrapper for the canonical deterministic benchmark suite.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

args=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    --data-dir)
      args+=(--output-dir "$2")
      shift 2
      ;;
    *)
      args+=("$1")
      shift
      ;;
  esac
done

exec "$REPO_ROOT/benchmarks/deterministic/run.sh" "${args[@]}"
