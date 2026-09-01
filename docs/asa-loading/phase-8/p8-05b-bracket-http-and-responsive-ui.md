# P8-05b: Bracket HTTP and responsive UI

## Control

- Status: Complete
- Intended implementation model: Luna
- Required review: Terra
- Depends on: P8-05a
- Blocks: P8-06

## Goal

Render verified cached brackets at knockout stage roots while retaining factual
chronology at `/fixtures`.

## Fixed decisions

- A stage with `CapabilityBracket` uses its root as the bracket landing page;
  `/fixtures` remains chronological results/schedule.
- Render verified structure even when empty. Fill only cached teams, dates,
  scores, xG, extra-time labels, and shootout tallies. Use `TBD` for unknown
  slots and explicit advancement text in the semantic markup.
- Empty, partial, ready, and unresolved are normal displays. Format mismatch is
  a nonfatal warning with a link to fixtures and the source games still shown.
- Desktop uses connected ordered round columns. At 390px use stacked semantic
  rounds with no horizontal page overflow and explicit `Advances to …` text;
  connectors are decorative and hidden from assistive technology.
- Preserve relative URLs, cache-only requests, regular-season pages, and all
  capability gates.

## Allowed changes

- `internal/app/` handler/view/template/static files and tests
- Packet/index status and active UI documentation

## Verification

```sh
go test -count=1 ./internal/app ./internal/bracket
golangci-lint fmt ./...
make lint
make vet
make test
govulncheck ./...
git diff --check
```

Visual acceptance: empty, partial, completed historical bracket, penalties,
desktop columns, and 390px stacked layout.
