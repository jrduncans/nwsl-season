# Evaluation evidence contract

## Data and selection rules

- The runner audits each requested regular season.
- Every requested season must pass its audit for a standard evidence result.
- Each held-out final-test season needs at least 95% xG coverage.
- Development seasons can guide candidate design. Held-out final-test seasons alone decide the recommendation. Pooled results are context only.
- Daily UTC cutoffs prevent same-day leakage.
- A formula, prior, or weight changed after final-test inspection defines a new model version; it cannot reuse those final-test claims.

## Commands and output paths

Run the standard evaluator with `go run ./cmd/backtest` after required historical regular seasons are available in the shared cache. It writes `docs/model-evaluation-v1.json` and `docs/model-evaluation-v1.md` only when the complete audit/coverage contract succeeds.

For inspection, pass `-allow-incomplete` and both `-json` and `-markdown` paths outside checked-in evidence. Label the resulting report incomplete/diagnostic; it must not claim to replace the checked-in recommendation. `-generated-at` supports byte-stable reruns.

## Evidence replacement checklist

1. Confirm why regeneration is needed, particularly after a material historical correction marked evaluation evidence dirty.
2. Run a complete, non-diagnostic evaluation using the intended cache snapshot.
3. Confirm every audit and final-test coverage threshold.
4. Review Markdown and JSON together: generated timestamp/parameters, audit rows, window labels, metrics, paired comparisons, and selection conclusion must agree.
5. Replace both checked-in artifacts together only if the run is complete and the user requested evidence replacement.
