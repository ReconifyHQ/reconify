---
name: reconify-engine-debug
description: Diagnose Reconify Engine reconciliation exceptions and explain deterministic result artifacts.
---

# Reconify Engine Debug

Start with structured output, preferably `reconify reconcile --format ndjson`, and inspect the summary plus exception events. Classify whether the failure is input mapping, duplicate handling, reference matching, amount tolerance, date window, or a configured pass. Re-run with the smallest reproducer and finish with `reconify explain RESULT.json` for a deterministic, bounded summary.

Do not treat a lower match rate as a reason to widen tolerances without identifying the affected rows.
