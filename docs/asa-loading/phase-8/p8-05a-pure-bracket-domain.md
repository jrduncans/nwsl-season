# P8-05a: Pure bracket domain

## Control

- Status: Complete
- Intended implementation model: Terra
- Required review: Sol
- Depends on: P8-04
- Blocks: P8-05b, P8-06

## Goal

Build an immutable bracket view from cached games and catalog topology without
persisting or inventing derived state.

## Fixed decisions

- Add a pure `internal/bracket` package. Inputs are a defensive copy of
  `competition.BracketFormat` plus cached games; output contains ordered
  source-backed rounds/nodes/slots and a closed state: `empty`, `partial`,
  `ready`, `unresolved`, or `format_mismatch`.
- Place games using verified format, chronological round ordering, known game
  count, observed participants, and the catalog's complete opening-round
  source-order permutation when one is present. Accept both timestamp layouts
  already supported by the shared ASA fixture parser. Never infer from status
  alone, matchday, home venue, expanded minutes, or a preferred team.
- A winner exists only for unequal regulation/extra-time scores or a valid
  paired shootout tally. Equal scores without valid penalties are unresolved.
- Fixed formats advance only winners along fixed connections. The 2021-2023
  playoff format accepts historically observed later-round participants and
  validates that they are the two bye seeds plus the two quarterfinal winners;
  it does not fabricate a pairing before those source games exist.
- Empty/partial brackets retain the complete verified shape and `TBD` slots.
  Extra source games, impossible participants, duplicate placement, or a game
  count/topology conflict produces a nonfatal format-mismatch result containing
  the source games and diagnostics.
- Support every bracket format in P8-01, including single Challenge Cup finals.

## Allowed changes

- New `internal/bracket/` package and tests
- Minimal cache-to-domain adapter under `internal/app` only if needed for type
  isolation; no rendering in this packet
- Packet/index status

## Verification

```sh
go test -count=1 ./internal/bracket
golangci-lint fmt ./...
make lint
make vet
make test
govulncheck ./...
git diff --check
```

## Stop conditions

- Stop if a historical row cannot be placed without one of the prohibited
  inference signals or if catalog metadata cannot represent the observed
  advancement truthfully.
