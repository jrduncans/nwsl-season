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
season's primary stage. Historical regular seasons carry verified fixture inventories and a
playoff place threshold (top four 2016–2019, six 2021–2023, eight 2024–2025, from NWSL
records), but no clinching rules. Completed knockout and
group stages carry exact match counts; 2026 Playoffs has a bracket format but no
inventory count while publication is incomplete.

### Verified historical regular-season inventories

These are published schedule sizes, not totals inferred from the ASA cache.
Awarded matches count toward the official inventory.

| Seasons | Teams | Matches per team | Matches | Evidence |
| --- | --- | --- | --- | --- |
| 2016 | 10 | 20 | 100 | [Reign schedule](https://medium.com/@ReignFC/the-wait-is-over-2016-season-schedule-released-287b959eea39), [tenth club announcement](https://www.orlandocitysc.com/news/orlando-pride-set-sunday-s-debut-match-portland) |
| 2017 | 10 | 24 | 120 | [Spirit schedule](https://washingtonspirit.com/2017/03/01/nwsl-announces-washington-spirit-2017-regular-season-schedule/) |
| 2018 | 9 | 24 | 108 | [League format announcement](https://www.nwslsoccer.com/news/nwsl-announces-boston-breakers-to-cease-operations) |
| 2019 | 9 | 24 | 108 | [Reign schedule announcement](https://www.reignfc.com/news/2019/2/22/reign-fc-announce-schedule-for-2019-nwsl-season-sp35b) |
| 2021 | 10 | 24 | 120 | [League schedule](https://www.nwslsoccer.com/news/nwsl-announces-full-2021-regular-season-schedule), [awarded-match evidence](historical-outcomes-audit.md#2021-two-awarded-matches-missing-from-asa) |
| 2022 | 12 | 22 | 132 | [League schedule](https://www.nwslsoccer.com/news/nwsl-announces-full-2022-regular-season-schedule) |
| 2023 | 12 | 22 | 132 | [League schedule](https://www.nwslsoccer.com/news/nwsl-announces-full-2023-season-schedule) |
| 2024 | 14 | 26 | 182 | [League format](https://www.nwslsoccer.com/news/national-womens-soccer-league-announces-2024-schedule-footprint-competition-calendar) |
| 2025 | 14 | 26 | 182 | [League competition rules](https://images.nwslsoccer.com/image/private/t_q-good/prd/kqbdfpjgywmisiyhhtnp.pdf) |

All completeness consumers use `competition.ReconcileInventory`: cache readiness,
Explore eligibility, season-page finality, Seeding history and full source-refresh
validation. Known awards fill only explained per-team deficits; awards already
represented in the source counts are skipped. Team, match and per-team totals
must all agree. Unexplained deficits remain incomplete.

Observed counts, cached fixtures and played-match statistics stay source-only.
For 2021, 118 cached played matches plus two documented awards satisfy the
120-match official inventory. Scoring distributions and xG averages still use
the 118 played matches; official standings and historical margins include awards.

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
stage slug for a cataloged season is a `404`, never a fallback. The main-page menu label is Standings for Regular Season and the catalog’s
short menu label for other competitions: Playoffs, Group stage, Knockout round,
or Final. When the main page renders chronological matches, the separate
Matches/Results item is omitted and `/fixtures` redirects there with its query
intact. A concrete matchup or a certified seeded Playoffs bracket restores the
separate Matches page. Every link and
redirect is relative so the `/nwsl-season/` proxy prefix survives.

Each stage page has in-page Season and Competition switchers whose targets are
server-rendered relative URLs; the global site header gains no season or stage
controls. Without JavaScript the switchers cannot navigate, so a `<noscript>`
block lists the same targets as plain links, keeping the current page (such as
`/fixtures`) where the target season has it.

Playoffs becomes discoverable when a concrete matchup is cached **or** the
same season's regular-season inventory is verified complete with safe final
results. Placeholder rows alone do not expose other knockout stages. Completed
regular-season standings link to the same season's playoff page and its
regular-season results. Pages that have already loaded the selected season's
regular-season data reuse it for this check; other pages read it from the cache.

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
A playoff page waiting on ASA says ASA hasn't published the schedule **or**
this site hasn't picked it up, because a cache miss cannot show what ASA
currently publishes. When the latest Playoffs refresh failed, the load-state
page and the seeded bracket, including a fully published one, warn that
published details may be newer than the page.

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
completion. Historical regular seasons with verified complete inventories and
terminal results show `Final standings`, including 2021 after award reconciliation.
Incomplete or unverified historical seasons retain `Historical standings`.
Presentation may hide a feature that is irrelevant to a complete season, but it
never enables one the catalog omits. Once safe final standings and a compatible bracket are available, Forecast Lab
moves from Regular Season to Playoffs. This separate availability rule reuses
the Regular Season forecast capability and never adds league-only capabilities
to knockout catalog entries.

## Knockout brackets

A stage with the bracket capability uses its root for a rendered bracket and
keeps `/fixtures` chronological. Before any concrete matchup or certified
Playoffs seeds, its root is the match list and `/fixtures` redirects there. The catalog holds each verified, versioned bracket
topology: rounds, slots, seed pairs, bye seeds, connections, advancement
policy (fixed or single final) and, for 2025 quarterfinals, the order in which
ASA published the opening matches. NWSL playoffs have never reseeded. The
six-team 2021–2023 brackets give seeds 1 and 2 byes: seed 1 hosts the 4–5
winner in Semifinal 1 and seed 2 hosts the 3–6 winner in Semifinal 2, as every
cached semifinal from those seasons shows. A bye-seed slot takes one prior
winner rather than two. ASA published each season's 4–5 quarterfinal first, so
chronological placement keeps it in Quarterfinal 1.

[`internal/bracket`](../internal/bracket/bracket.go) builds the view from that
format and cached matches. It places matches by format, round chronology, match
count, observed participants and source order, never by matchday, home venue,
status alone or expanded minutes. A winner exists only for unequal
regulation/extra-time scores or a valid paired shootout tally. Unknown slots are
`TBD`. The result state is `empty`, `partial`, `ready`, `unresolved` or
`format_mismatch`; a mismatch is a nonfatal warning that still shows the source
matches and links to the Matches page. Cards label each match and stack the two
teams, with aligned seeds, crests and full names. Upcoming outlooks put each
team's advance chance in its row under one Advance header, above an always-visible
three-part 90-minute win / extra time / win bar. The ends name the teams by code;
the top row always corresponds to the left segment, regardless of seed or
published venue order. The Matches list keeps side-by-side Home vs Away teams,
with each advance chance beneath its team name and the same code-labeled bar
below. Home owns the left segment, and the Knockout match tag stays with the
usual match metadata. Completed bracket cards put Goals and xG in separate
right-aligned columns in each team row, emphasize the winner, and mute the loser.
The xG column is omitted when neither side has a value; a single missing side
reads `—`. Cards show a header only when there are Goals or Advance columns,
so unresolved placeholders have no lone Team heading. Penalty shootout tallies
sit beside each team's goals (`2 (5)` versus `2 (4)`), with accessible text naming
the penalty decision and both tallies. After extra time remains below the rows.
Final seeds annotate team identities in every bracket round and in upcoming and
completed Matches-list rows, including historical results, only when safe
regular-season completion and the official total rank resolve that team's seed.
Unavailable seeds are omitted. These annotations never reorder published sides
or change historical match placement. Unresolved entrants name their feeder
match (for example, Winner of Quarterfinal 1); known entrants in an unplayed
match keep that feeder as a caption, which completed matches drop.
At desktop widths, later matches center between their feeders and elbow
connectors join each feeder to its destination team row. Layout measures card
heights so wrapped names and match outlooks cannot overlap. Fixed paths follow
the catalog, whatever the kickoff order. A bye semifinal accepts its prior
winner against a team absent from earlier rounds, and its bye side reads
First-round bye while unplayed. At 390px rounds
stack in round order with feeder labels. Without JavaScript, desktop rounds
spread their cards over a shared height and omit connectors.

Round-jump buttons accompany the stacked phone bracket and any bracket that
overflows horizontally. On phones they jump to a round heading; on wider
screens they scroll the bracket to that round. A horizontal-scroll hint appears
only when content actually overflows. Full-width desktop brackets keep their
existing layout without extra controls. These navigation aids require JavaScript.

Once the regular season is safely complete, Playoffs can show opening-round
expectations before matches arrive. `bracket.BuildWithSeeds` combines the
catalog's verified seed pairs with resolved official **total** standings.
Unavailable adjustments or undetermined tiebreaks leave affected seeds TBD;
an abandoned match, incomplete inventory, missing score or unknown status cannot
certify entrants. Cards show seeds, kickoff (or Date TBD), and match outlooks when both teams are known.
Expectations exist only on the page; placeholder matches supply no date or
participants. Once ASA has published every match, the publication note disappears.

With this seed context, partially published matches are matched by participants,
not by publication or kickoff order. Published sides and results take precedence
in matched slots; only source results supply winners and later advancement.
Games that disagree with resolved seed pairs or cannot be placed against known
advancement produce visible diagnostics and a chronological published-match
fallback on the playoff page, with a link to the Matches page.
A game naming a team without a resolved seed is not a disagreement: it fills the
one opening-round slot whose resolved sides it includes, provided its other
participant holds no resolved seed. If several slots could hold it, it stays
unplaced, the bracket is unresolved, and the published-match list shows it.
Populated historical brackets retain the existing source-backed interpretation,
including playoff evidence for tied historical ranks.

Group stages render chronological matches with date headings, never
`Matchday` headings or a combined table.

### Forecast use of postseason state

Current-season Forecast Lab reuses the seeded bracket interpretation after
verified, safe regular-season completion. Resolved final seeds and the verified
format permit forecasting before fixture publication. Cached completed results,
including confirmed shootout advancement, fix winners; only unresolved rounds
are sampled. Published home/away assignments inform quarterfinal and semifinal
venue effects, while the designated-site final remains neutral in the model.
Published kickoffs are linked from the bracket; playoff recovery and load
adjustments are omitted and model fitting stays regular-season-only.

Unresolved playoff seeds, ambiguous results, missing advancement, or mismatched
source games withhold championship odds with a visible explanation. They do not
silently restart rounds. Final points, qualification, and recorded playoff
eliminations remain factual where provable. Playoff source fields participate in forecast cache identity and
current-season playoff changes trigger baseline warming. Page requests stay
cache-only. See [Forecast Lab](forecast-lab-guide.md#championship-forecasting-after-the-regular-season)
for the full evidence boundary and stale-scenario recovery.

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
