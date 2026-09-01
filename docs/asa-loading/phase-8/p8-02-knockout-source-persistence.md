# P8-02: Knockout source persistence

## Control

- Status: Complete
- Intended implementation model: Terra
- Required review: Sol
- Depends on: P8-01
- Blocks: P8-03 through P8-06

## Goal

Persist ASA's normalized extra-time and shootout facts losslessly so later
bracket code can determine winners without interpreting raw JSON.

## Fixed decisions

- Migrate schema version 13 to 14 in the existing transaction. Add nullable
  `extra_time` and `penalties` boolean columns plus nullable nonnegative
  `home_penalties` and `away_penalties` integer columns to `games`.
- The cache representation uses `sql.NullBool` for both booleans and
  `sql.NullInt64` for both scores. ASA's wire representation uses pointers for
  all four fields so absent remains distinct from false/zero.
- Extra time and penalties are independent. A valid row may have
  `penalties=true` with `extra_time` absent or false. When `penalties=true`,
  both penalty scores must be present and nonnegative. When penalties is false
  or absent, both scores must be absent. A lone score, negative score, or
  incompatible source combination rejects the complete operation before
  durable mutation.
- Authorized ASA sentinel compatibility: when penalties is false or absent and
  both supplied penalty scores are exactly zero, normalize both cache scores to
  NULL while preserving `raw_json`; do not infer a shootout or winner. Every
  lone, negative, or nonzero score remains incompatible.
- Migration backfills only from valid `raw_json`. Missing fields remain NULL.
  Malformed JSON remains untouched. A valid-but-incompatible combination is an
  ambiguous legacy row and aborts migration rather than guessing. Refresh each
  affected current fixture snapshot identity after normalization.
- Include all four fields in mapping, validation, SQL insert/update/select,
  equality, freshness/material-change classification, targeted/full audit
  paths, planning snapshots, and defensive copies.
- Add old/new presence and value telemetry attributes for all four fields to
  the registry and generated conventions. Values are low-cardinality booleans
  and bounded match scores; no raw JSON is emitted.
- Preserve schema-13 idempotence behavior when columns already exist, and do
  not rebuild or delete the games table.

## Allowed changes

- `internal/asa/` game wire type and tests/testdata
- `internal/cache/` game persistence, migration, clone/compare/audit code and
  focused tests
- `internal/syncer/` mapping, freshness attributes, and focused tests
- `telemetry/registry/sync.yaml` and generated telemetry convention files
- This packet and the packet index for status handoff

Do not edit competition topology, scheduler policy, commands, application
routes/templates, or bracket code.

## Required behavior and tests

- Fresh schema 14 and a realistic schema-13 upgrade both preserve all existing
  rows, foreign keys, audits, readiness, and derived data.
- Migration tests cover missing fields, valid false values, extra time without
  penalties, direct penalties without extra time, full shootout data, malformed
  JSON, and every invalid paired-score combination.
- Mapping/persistence tests cover round trip, update/material classification,
  targeted refreshes, full inventories, and unchanged rows.
- Generated registry checks pass with the new old/new fields.

## Verification

```sh
go test -count=1 ./internal/asa ./internal/cache ./internal/syncer
golangci-lint fmt ./...
make telemetry-check-generated
make telemetry-live-check
make lint
make vet
make test
govulncheck ./...
git diff --check
```

## Non-goals

- Scheduler discovery or catalog backfill.
- Bracket construction, winner inference, or UI.
- Persisting derived bracket state.

## Stop conditions

- Stop on any migration loss, ambiguous valid legacy data, or incompatible
  value observed from the live source contract.
- Stop if schema 14 would require destructive table replacement.

## Handoff

Report files changed, migration/backfill behavior, validation decisions,
generated telemetry changes, all verification outcomes, deviations, and open
questions. Set the packet to `Review`; the primary advances it only after Sol
accepts the diff.
