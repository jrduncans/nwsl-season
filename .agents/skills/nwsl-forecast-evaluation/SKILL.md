---
name: nwsl-forecast-evaluation
description: Safely run, diagnose, review, or update NWSL forecast-model evaluation evidence. Use for backtests, model comparison, incomplete season/xG audits, diagnostic output paths, development versus final-test separation, replacement decisions, or checked-in model-evaluation evidence; do not use for ordinary Forecast Lab behavior or unscoped model changes.
---

# NWSL forecast evaluation

Protect the historical evaluation protocol and its checked-in evidence. Before evaluation work, read `README.md`, `docs/forecast-lab-guide.md`, `docs/model-evaluation-v1.md`, and the relevant evaluator code. For synchronization-driven data issues, also read `docs/sync-logic-guide.md` and use `nwsl-sync-investigation` when needed.

## Safe workflow

1. State the purpose: diagnostic, candidate-model development, final-test evaluation, or evidence regeneration after an eligible historical correction.
2. Audit every requested season before interpreting scores. A standard evidence run requires every requested season to pass audit and every final-test season to have at least 95% xG coverage.
3. Preserve the split: development seasons may guide candidate design and fixed constants; held-out final-test seasons alone determine whether a candidate replaces the selected model. Pooled results are descriptive only.
4. Preserve daily UTC cutoffs so same-day data cannot train a forecast for another game that day. A model changed after inspecting final-test results is a new version and needs later untouched final-test data to make a new final-test claim.
5. Choose the output path before running. Use `-json` and `-markdown` paths outside checked-in evidence for diagnostics; use `-allow-incomplete` only with those clearly labeled diagnostic outputs. Do not overwrite `docs/model-evaluation-v1.{md,json}` with partial results.
6. Replace checked-in evidence only after the complete standard runner succeeds and the new artifact records the reproducible run. Review both Markdown and JSON for the same result, audit, and selection conclusion.

Use [the evidence contract](references/evidence-contract.md) for command and replacement details.

## Report discipline

Lead with audit eligibility, then development and final-test results separately, then the precommitted selection result. Name excluded/incomplete seasons and diagnostic-only output prominently. Do not let a successful diagnostic or pooled score be represented as a model-selection result.

## Scope and safety

The evaluator reads the shared persistent cache. It does not authorize source refreshes, historical backfill, or production repairs. Ask for explicit authorization before any operation that would change production cache data; otherwise diagnose with existing cache/evidence or write only to caller-approved diagnostic paths.
