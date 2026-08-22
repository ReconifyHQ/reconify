---
name: reconify-engine-bootstrap
description: Take a new reconciliation from two unfamiliar files to a validated config and a first explained result. Use when there is no reconify.yaml yet.
---

# Reconify Engine Bootstrap

Zero to a first trustworthy result. For the full graded workflow see `reconify-engine-reconcile`;
for key-by-key configuration detail see `reconify-engine-config`.

## 1. Learn the build and the data

```bash
reconify capabilities
reconify inspect left.csv
reconify inspect right.csv
```

`inspect` gives you each column's `inferred_type`, `sample_values`, and the exact `date_layout` for
date columns. Copy those values; do not guess them.

## 2. Ask the questions the files cannot answer

Before writing YAML, settle with the user:

- Which column is the reliable shared identifier? Descriptions and memo text usually repeat across
  unrelated payments; a system-issued reference usually does not.
- Are the amounts expected to differ, and by how much at most? That threshold becomes
  `amount_tolerance_minor`, **in minor units** — 2.00 with `multiplier: 100` is `200`.
- Can the same transaction appear on different dates on each side? That becomes `date_window`.

If you cannot get answers, state the assumption you made in your summary rather than burying it in
the config.

## 3. Write and prove the config

```bash
reconify config schema                       # every key, typed
reconify config validate --config reconify.yaml
reconify config check-source --config reconify.yaml --source left --file left.csv
reconify config check-source --config reconify.yaml --source right --file right.csv
```

`file_pattern` is a glob resolved relative to the config file's own directory, not your working
directory.

## 4. First run

```bash
reconify reconcile --config reconify.yaml --pair left_vs_right \
  --format json --deterministic --out result.json
reconify explain result.json
```

Use `--format ndjson` instead for large files, so memory stays bounded and each event is on its own
line.

## 5. Sanity-check before reporting

Compare the summary against what the user described. If they mentioned a missing payment and
`unmatched_left` is `0`, the config is absorbing it — most often through a tolerance or date window
that is too wide. Keep the validated config and the result file together; they are what makes the
number reproducible.
