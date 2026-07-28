# Project roadmap

This roadmap divides the site into small, runnable learning projects. A phase is
complete when its exit criteria pass; it does not need every possible refinement.

## Working principles

- Keep domain calculations independent of HTTP, ASA, and SQLite.
- Store ASA data locally so page requests do not depend on ASA being available.
- Make synchronization idempotent: running it twice should produce the same data.
- Preserve enough source data to debug changes in ASA responses.
- Prefer tests built from tiny invented seasons before tests built from live data.
- Record rule assumptions, especially playoff format and tiebreakers, by season.

## Phases

1. **Go and HTTP scaffold** — Run a minimal server and learn the repository shape.
   See [`01-scaffold.md`](01-scaffold.md).
2. **ASA client** — Fetch and decode games without involving a database.
   See [`02-asa-client.md`](02-asa-client.md).
3. **SQLite persistent cache** — Store normalized games and safely refresh them.
   See [`03-sqlite-cache.md`](03-sqlite-cache.md).
4. **Standings** — Calculate a table from completed games.
   See [`04-standings.md`](04-standings.md).
5. **Clinching** — Prove whether any feasible set of results can deny a playoff
   place. See [`05-clinching.md`](05-clinching.md).
6. **Website and what-ifs** — Render the season and let users choose hypothetical
   outcomes. See [`06-website-and-what-if.md`](06-website-and-what-if.md).
7. **Strength of schedule** — Add transparent alternative measures.
   See [`07-strength-of-schedule.md`](07-strength-of-schedule.md).
8. **Operations** — Refresh the current-season cache from the server process,
   expose freshness, and deploy. See [`08-operations.md`](08-operations.md).
9. **Season overview and schedule difficulty** — Make remaining schedule
   difficulty visible in the primary league view and move its dense supporting
   detail into an exploratory presentation. See
   [`09-season-overview.md`](09-season-overview.md).
10. **Forecast-first simulator** — Replace the fixture-by-fixture what-if form
    with a useful default season simulation and optional fixed-result
    assumptions. See [`10-forecast-simulator.md`](10-forecast-simulator.md).
11. **xG and model comparison** — Cache game-level xG, back-test forecast models,
    select an opinionated default, and let interested users compare alternatives.
    The audited v1 evaluation selects `xg-poisson-v1` as the current default.
    See [`11-xg-and-models.md`](11-xg-and-models.md).
12. **Season-scale clinching status** — Generalize exact qualification proofs to
    the Shield, a top-four seed, and a playoff place; use cheap bounds and a
    coupled-fixture optimizer so status can be calculated throughout the season.
    See [`12-season-scale-clinching.md`](12-season-scale-clinching.md).
13. **Clinching scenarios** — Turn the qualification proof into minimal,
    reporter-style conditions for an upcoming slate, and present both current
    status and future opportunities without crowding the standings. See
    [`13-clinching-scenarios.md`](13-clinching-scenarios.md).

## Current clinching guide

For a layered description of the live proof, persistence, no-help, and
next-slate scenario behavior, see
[How clinching works](../clinching-logic-guide.md).

## Current Forecast Lab guide

For a layered description of the model catalog, complete-season simulation,
fixed assumptions, comparisons, and shareable scenario behavior, see
[How Forecast Lab works](../forecast-lab-guide.md).

The order is intentional, but not sacred. Styling can happen whenever it keeps
the project fun. The original dependency chain is games → cache → standings
→ clinching. The next sequence is schedule context → forecast simulation →
validated model comparison → season-scale qualification proofs → explainable
match-week scenarios. Phase 9 can reuse the existing schedule-strength domain
result; Phases 10 and 11 should preserve the same separation between domain
calculations, persistence, and HTTP presentation. Phase 12 replaces the current
four-fixture clinching gate with bounded optimization, and Phase 13 depends on
that reusable proof rather than introducing a second scenario calculator.

## Decisions to revisit

- Which SQLite driver to use. Prefer a pure-Go driver unless measurements show a
  reason to accept CGO.
- What ASA's post-match publication delay is, and whether the initial
  three-hour completion grace needs adjustment.
- How official tiebreak rules differ by season.
- Which optimization approach best handles coupled-fixture qualification proofs
  after the Phase 12 points bounds have reduced the problem.
- How to model the score-based tiebreak frontier without treating a small set of
  representative scorelines as an exhaustive mathematical domain.
- Whether ASA or another maintainable source can supply disciplinary points, the
  first official tiebreak input not currently available in the cache.
- Scenario state uses versioned URLs. Forecast URLs also include the model ID
  and intentionally resolve against the latest cache rather than preserving a
  historical source snapshot.
- Whether schedule difficulty remains useful as a dedicated page after its most
  important signals are incorporated into the season overview.
- How often the xG Poisson recommendation, selected by the v1 walk-forward
  evaluation, should be reconsidered as new completed seasons arrive.
- Whether validated team-strength estimates should later appear as a separately
  named power rating. They must never replace or silently adjust official
  standings.
