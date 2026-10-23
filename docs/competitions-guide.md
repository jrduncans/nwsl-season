# How competitions, seasons and stages work

Every page is scoped to one season and stage, the pair ASA filters by. The
[competition catalog](../internal/competition/catalog.go) decides which pairs
are public and what each one may show; the cache decides what has been loaded.
Neither invents facts the other lacks.

## The catalog

A catalog `Entry` describes one season/stage: its label and URL slug, its kind
(`league_table`, `knockout` or `group`), whether it is public and the season's
primary stage, an optional verified inventory (`Teams`, `GamesPerTeam`,
`Games`), optional verified competition `Rules`, optional bracket format, and
an explicit capability list.

Public stages, in catalog order within a season:

| Family | Seasons | Kind | Capabilities |
| --- | --- | --- | --- |
| Regular Season | 2026 | league table | fixtures, standings, xG, schedule difficulty, forecast, qualification, scenarios |
| Regular Season | 2016–2019, 2021–2025 | league table | fixtures, standings, xG |
| Playoffs | 2016–2019, 2021–2026 | knockout | fixtures, xG, bracket |
| Challenge Cup Group Stage | 2020–2023 | group | fixtures, xG |
| Challenge Cup Knockout Round | 2020–2023 | knockout | fixtures, xG, bracket |
| Challenge Cup Final | 2024–2026 | knockout | fixtures, xG, bracket |

ASA has no 2020 regular season, so the 2020 Challenge Cup group stage is that
season's primary stage. Historical regular seasons carry only a verified
playoff place threshold (top four 2016–2019, six 2021–2023, eight 2024–2025, from NWSL
records), not inventory, tiebreak or clinching rules. Completed knockout and
group stages carry exact match counts; 2026 Playoffs has a bracket format but no
inventory count while publication is incomplete.

### Capabilities are explicit

A page or control appears only when the entry lists its exact capability.
Nothing is inferred from stage kind, rules, inventory, cached rows or another
capability. Two features also need verified rules: Forecast Lab needs rules
with a playoff season outcome, and clinching needs qualification, scenarios and
rules. Validation forbids league-only capabilities on non-league stages and
bracket support without a knockout format.

Where a fact is not verified, the page omits it rather than guessing: no
playoff line without a verified place threshold, no expected-count claims without inventory,
and no rule-dependent calculation without rules. An uncataloged season with
cached matches is factual-only: results and matches with xG, plus a notice that
the competition format is not verified.

A direct request for an unsupported feature returns a rendered `404` that names
the feature, keeps the stage's factual navigation, and links back to the
season. The capability check runs before any cache read or calculation, so an
unsupported request has no side effects.

## URLs and navigation

Season pages live at `/seasons/{season}/{stage-slug}` with optional
`/fixtures`, `/schedule-difficulty`, `/forecast`, `/model-evaluation` and
`/clinching` suffixes. The root redirects to the configured current season's
primary stage (`NWSL_SYNC_SEASON`; it never rolls over by calendar), and so does
the retired `/seasons` archive URL. Season-only
URLs redirect to the primary stage, keeping the suffix and query. An unknown
stage slug for a cataloged season is a `404`, never a fallback. Every link and
redirect is relative so the `/nwsl-season/` proxy prefix survives.

Each stage page has in-page Season and Competition switchers whose targets are
server-rendered relative URLs; the global site header gains no season or stage
controls. Without JavaScript the switchers cannot navigate, so a `<noscript>`
block lists the same targets as plain links, keeping the current page (such as
`/fixtures`) where the target season has it.

## Readiness and completeness

`cache.SeasonReadiness` reports two independent facts from one read-only
snapshot:

- **Readiness**: `available` once any match is cached or ASA discovery found
  inventory, `not_published` when ASA returned no inventory, otherwise
  `unknown`.
- **Completeness**: compares cached matches with the verified inventory; it is
  `unknown` without one, otherwise `complete` or `incomplete`.

A public stage with no cached matches renders a normal `200` load-state page
("not loaded" or "not published") and never contacts ASA.

## Competition status

The app classifies a loaded league-table stage for presentation only (no cache
state):

| Phase | Condition | Presentation |
| --- | --- | --- |
| `upcoming` | Only scheduled matches | Points to the schedule instead of an all-zero table. |
| `active` | Completed and scheduled matches | The full explorer. |
| `complete` | Every match terminal and the verified inventory satisfied | `Final standings`, totals by default, no remaining-schedule UI, results without an Upcoming toggle. |
| `unknown` | Anything else | Same as active. |

`FullTime` and `Abandoned` are terminal; an unknown status never proves
completion. Historical regular seasons have no inventory, so they show
`Historical standings` with retrospective defaults but never claim finality.
Presentation may hide a feature that is irrelevant to a complete season, but it
never enables one the catalog omits.

## Knockout brackets

A stage with the bracket capability uses its root for the bracket and keeps
`/fixtures` chronological. The catalog holds each verified, versioned bracket
topology: rounds, slots, seed pairs, connections, advancement policy (fixed,
historically reseeded, or single final) and, for 2025 quarterfinals, the order
in which ASA published the opening matches.

[`internal/bracket`](../internal/bracket/bracket.go) builds the view from that
format and cached matches. It places matches by format, round chronology, match
count, observed participants and source order, never by matchday, home venue,
status alone or expanded minutes. A winner exists only for unequal
regulation/extra-time scores or a valid paired shootout tally. Unknown slots are
`TBD`. The result state is `empty`, `partial`, `ready`, `unresolved` or
`format_mismatch`; a mismatch is a nonfatal warning that still shows the source
matches and links to the Matches page. At 390px rounds stack with explicit `Advances to`
text. When a match card is narrower than 20rem (three columns on a tablet or
narrow window), each team shows its short code instead of its full name, as
Matches rows do on phones; the full name stays available to screen readers.

Group stages render chronological matches with date headings, never
`Matchday` headings or a combined table.

## Derived historical outcomes

Explore thresholds derive official points and matches from ASA results, and
qualification and seed evidence from cached playoff matches. The historical
regular-season archive includes playoff matches read in the same SQLite
transaction; they never contribute to regular-season scoring totals. The
existing playoff catalog supplies topology, seed pairs and verified source order,
so formats are not duplicated in a separate outcome catalog.

[`outcome_overrides.json`](../internal/competition/outcome_overrides.json) supplies
only facts ASA matches cannot establish: awarded matches, point deductions and
otherwise ambiguous tied orders. Ordinary completed seasons require no entry.
The [exception audit](historical-outcomes-audit.md) explains each current override
and the evidence constraints. The shared standings module selects the historical policy for each season;
unknown seed order after computable rules and playoff evidence remains a gap.

This derivation does not enable historical forecasting or clinching capabilities
and does not change catalog inventory or season-page finality. Existing normal
catalog work still applies when a new season or bracket format is introduced.
