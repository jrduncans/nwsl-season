# P8-01: Competition catalog

## Control

- Status: Complete
- Intended implementation model: Luna
- Required review: Terra
- Depends on: Phase 7
- Blocks: P8-02 through P8-06

## Goal

Represent every supported ASA-backed NWSL competition stage, together with
the verified immutable topology needed to build source-backed knockout
brackets in later packets.

## Fixed decisions

- Extend `competition.Entry` with `ShortLabel string` and
  `BracketFormat *BracketFormat`. `ShortLabel` is the visitor-facing stage
  name without the season. It is required on every entry. A bracket format is
  allowed only on knockout entries and requires `CapabilityBracket`.
- Define versioned immutable catalog metadata in `internal/competition`:
  `BracketFormat`, `BracketRound`, `BracketSlot`, `BracketConnection`, and a
  closed `AdvancementPolicy` type. The format records ordered rounds, ordered
  slots, optional two-seed pairs, connections, and whether advancement is
  fixed, historically observed/reseeded, or a single final. An opening slot
  may also carry a verified `SourceOrder` when ASA's chronological publication
  order differs from topology order, as it does for the 2025 quarterfinals.
  Version is `1`.
- Validation rejects blank/duplicate IDs, unknown advancement policies,
  invalid seed pairs, connections to missing or same/earlier-round slots,
  duplicate connection destinations, bracket capability without a format, and
  a format without bracket capability. Source order is allowed only for the
  opening round and must be either omitted from every opening slot or form a
  complete contiguous `1..N` permutation. Copying any catalog result must
  deeply copy the complete format graph.
- Keep regular-season stages unchanged except for `ShortLabel: "Regular
  Season"`.
- Add public, source-backed stages with canonical slugs and exact known game
  inventories:
  - `Playoffs`, slug `playoffs`: 2016-2019 have 3 games (semifinals then
    final); 2021-2023 have 5 (two quarterfinals, two semifinals, final) and use
    historically observed/reseeded semifinal advancement; 2024-2025 have 7
    (four quarterfinals, two semifinals, final) with fixed 1-8/4-5 and 2-7/3-6
    sides. Add the same verified seven-slot 2026 format without a fixed
    inventory count because publication is not complete.
  - `NWSL Challenge Cup Group Stage`, slug
  `challenge-cup-group-stage`: 2020=16, 2021=20, 2022=36, 2023=36 games.
    These are factual group stages with fixtures and xG only. The 2020 entry is
    that season's public primary stage.
  - `NWSL Challenge Cup Knockout Round`, slug
    `challenge-cup-knockout-round`: 2020=7 (quarterfinals, semifinals, final),
    2021=1 (final), 2022=3 and 2023=3 (semifinals, final).
  - `NWSL Challenge Cup Final`, slug `challenge-cup-final`: one-game final in
    each of 2024, 2025, and 2026.
- All knockout entries have exactly fixtures, xG, and bracket capabilities.
  Group entries have exactly fixtures and xG. They do not gain standings,
  qualification, forecast, schedule-difficulty, or scenarios.
- Challenge Cup short labels omit the redundant `NWSL` prefix: `Challenge Cup
  Group Stage`, `Challenge Cup Knockout`, and `Challenge Cup Final`.
- Deterministic ordering is season descending, primary first, then competition
  family in this order: Regular Season, Playoffs, Challenge Cup Group Stage,
  Challenge Cup Knockout Round, Challenge Cup Final. Do not use display-label
  alphabetization as the catalog's semantic ordering.
- Catalog metadata encodes verified structure, never inferred teams or match
  outcomes. Slots not determined by a seed pair or fixed connection remain
  placeholders for later source-backed construction. The 2025 opening order
  maps ASA's published chronological games to the immutable 1-8/4-5 and
  2-7/3-6 topology without treating kickoff time, home venue, or matchday as a
  seed fact.

## Allowed changes

- `internal/competition/catalog.go`
- `internal/competition/catalog_test.go`
- New focused files and tests under `internal/competition/`
- `internal/cache/source_scopes_test.go` only to make the existing catalog
  registration assertion cover the expanded source catalog
- `internal/app/handler.go` only for a temporary regular-season archive guard;
  P8-04 removes it when stage-appropriate archive links are implemented
- This packet and `docs/asa-loading/README.md` only for status handoff

Apart from the two compatibility/test exceptions above, do not edit
application or cache files. Do not edit syncer, scheduler, command, telemetry,
or template files.

## Required behavior and tests

- Table-driven tests cover every added stage's source name, slug, short label,
  kind, primary flag, inventory, capabilities, format version, round counts,
  slot counts, seed pairs, connections, and advancement policy.
- Tests prove 2020 primary lookup, all-season/public-stage deterministic order,
  deep-copy isolation, and validation failures for malformed formats.
- Existing regular-season lookup, routing, capability, and copy tests remain
  valid after their explicit expectations are updated.

## Verification

```sh
go test -count=1 ./internal/competition
golangci-lint fmt ./...
make lint
make vet
make test
govulncheck ./...
git diff --check
```

## Non-goals

- Source decoding or schema changes.
- Scheduler or backfill behavior.
- Application routes, archive presentation, fixtures, or bracket rendering.
- Persisting bracket state or guessing winners.
- Summer Cup support.

## Stop conditions

- Stop if an official competition format cannot be reconciled with the ASA
  stage rows and recorded inventory.
- Stop if representing a verified format would require inferred teams,
  results, venues, or source facts not present in the catalog contract.

## Handoff

Report files changed, the catalog/topology implemented, verification results,
deviations, and unresolved questions. Set this packet to `Review`; the primary
agent advances it only after Terra accepts the diff.
