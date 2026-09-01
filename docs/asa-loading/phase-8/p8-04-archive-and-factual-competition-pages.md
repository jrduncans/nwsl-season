# P8-04: Archive and factual competition pages

## Control

- Status: Complete
- Intended implementation model: Luna
- Required review: Terra
- Depends on: P8-03
- Blocks: P8-05a through P8-06

## Goal

Make every public catalog stage discoverable and render each non-bracket stage
as truthful cached facts without exposing league-only features.

## Fixed decisions

- `/seasons` groups all public stages under each season, season descending and
  catalog order within a season. Each stage has its own readiness label and
  canonical root and fixtures links as its capabilities permit.
- Remove P8-01's temporary regular-season archive guard.
- Render a semantic, keyboard-accessible in-page competition switcher on every
  season-stage page. Season switchers preserve the same canonical competition
  slug only in seasons where it exists. All targets are server-rendered
  relative URLs; JavaScript never interprets arbitrary option values as URLs.
- Group-stage roots and `/fixtures` render the same factual chronological
  fixture/result model with scores and xG. They never render combined
  standings, qualification, forecasts, scenarios, or schedule difficulty.
- Fixture group headings use verified competition labels: regular season may
  use Matchday; knockout uses catalog round labels when a cached row can be
  placed without guessing, otherwise `Results`/`Schedule`; group stages use
  chronological date/month labels and never `Matchday 27`-style headings.
- Preserve canonical route shapes, reverse-proxy relative URL behavior,
  current regular-season behavior, historical presentation, and 390px layout.

## Allowed changes

- `internal/app/` handlers, views, templates, static CSS/JS, and tests
- Active README/guide and packet status/index

Do not change schema, source mapping, scheduler, catalog topology, or build a
derived bracket.

## Verification

```sh
go test -count=1 ./internal/app
golangci-lint fmt ./...
make lint
make vet
make test
govulncheck ./...
git diff --check
```

Visually verify archive, group stage, competition/season switchers, historical
results, desktop, 390px, and a reverse-proxy path.

## Stop conditions

- Stop rather than show league-derived data for a non-league stage or infer a
  round from unsupported source fields.
