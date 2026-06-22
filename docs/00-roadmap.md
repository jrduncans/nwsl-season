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
8. **Operations** — Automate refreshes, expose freshness, and deploy.
   See [`08-operations.md`](08-operations.md).

The order is intentional, but not sacred. Styling can happen whenever it keeps
the project fun. The key dependency chain is games → cache → standings → clinching.

## Decisions to revisit

- Which SQLite driver to use. Prefer a pure-Go driver unless measurements show a
  reason to accept CGO.
- Whether refresh runs inside the web process, as a separate command, or both.
- How official tiebreak rules differ by season.
- Whether exact clinching needs a custom search or an optimization solver.
- Whether what-if state belongs in the URL, browser storage, or server sessions.
