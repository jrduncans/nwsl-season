# Data explorer: adding tools without redesigning the product

Status: revised proposal for review, September 8, 2026. Companion to
[the shippable-release design](shippable-redesign.md). This replaces the earlier
History roadmap in this file. The examples here are tests of extensibility and
possible additions, not an approved feature queue. The product priority is to
make a new useful exploration tool straightforward to add.

## The durable product structure

Data explorer is a library of purpose-built analytical tools. It supports
current-season and historical data equally. The dedicated season experiences
remain outside that library: Standings, Results & fixtures, Schedule difficulty,
Clinching scenarios, and Forecast lab keep their purpose and navigation.
Do not move Schedule difficulty merely because it could be classified as data
curiosity. Integration means easy entry and return, not consolidating every
page into a dashboard framework.

The stable structure is:

```text
Shared header on every page
├── Season context / Current season → existing dedicated season pages
├── All seasons → season-and-stage catalog
└── Data explorer
    ├── Tool catalog: available tools with descriptive visual previews
    └── Tool workspace
        ├── All tools / tool switcher
        ├── Tool-specific data controls
        ├── Chart / Table when both are useful
        └── One main analytical surface with direct value inspection
```

Adding a tool adds an entry to this structure. It does not add a product-level
navigation item or require an existing tool to acquire every new concept.
Tool controls may differ, while shared typography, control placement, exact-
value interaction, table sorting, URL behavior, and accessibility stay familiar.

## Tool boundaries: neither one page per idea nor a universal builder

Group related analyses when they use the same observations and relationship.
A measure choice can switch a team comparison from GD/xGD to points/xPoints.
A season selector can reuse it for last year. Those do not require new tools.

Create a separate tool when the analytical task or main interaction changes.
A match record list, season trajectory, and team comparison may need different
inputs and presentations. Making all of them fit six mandatory dropdowns would
be harder to use and more expensive to extend than sharing smaller components.

Use this rule during planning: **can the user understand the proposed change
as a different setting of the current analysis?** If yes, extend its supported
controls. If not, add a named tool with a useful default. Chart types are chosen
for the task; a generic chart-type menu is not the extension mechanism.

A curated preset can link to a useful combination of tool inputs. For example,
a Shield-winner comparison might eventually be a preset of a cross-season
team comparison. It need not be a separate top-level page, a hardcoded product
category, or a requirement imposed on all other tools.

## A small implementation contract

Keep a small explicit registry of shipped tools. The initial tools are Team
performance, Scoring trends, and Match goal distribution. Each entry describes:

| Contract | Responsibility |
| --- | --- |
| Stable ID, title, description, preview | Catalog discovery, direct route, and tool switcher |
| Supported context and defaults | Which season/stage/team/range inputs make sense; how contextual entry maps to them |
| Parameter parsing and canonical state | Validation, defaults, shareable URLs, reload and Back/Forward behavior |
| Data requirements and loader | Cache-only coherent inputs, independent capabilities, coverage rules |
| Pure calculation / query result | Observations, raw values, units, sample counts, and availability independent of rendering |
| Presentations | Chart or custom analytical surface; optional meaningful table and its column/sort definitions |
| Verification cases | Normal, missing-data, interaction, responsive, and state-restoration checks |

Common components own page chrome, the catalog, tool selection, standard
controls, chart inspection mechanics, numeric-table behavior, error/loading
states, and links back to relevant season pages. Tools own the meaning of their
inputs, axes, rows, eligibility, default ordering, and calculated values.

Support custom presentation content within the shared workspace from the
start. A future match list should not pretend to be a Cartesian chart to fit
an interface. Likewise, do not build a plugin runtime, expression language,
user-configurable dashboard, universal scope engine, or generic query-builder
backend before there is evidence for one.

Reuse the current coherent archive reader and pure history calculations where
they fit, without forcing current-season tools to depend on historical winner
metadata. Package names such as `internal/history` can remain implementation
details initially. Rename or extract shared calculations when actual reuse
makes the boundary clearer; a URL/name redesign alone does not justify a
large source-tree rewrite.

## What adding one tool should look like

1. State the analytical task, observation grain, useful default, and supported
   inputs in a short tool specification. Identify what is new versus reusable.
2. Confirm those inputs exist in the cache and define completeness for that
   analysis. Add a narrow pure aggregate only if one is missing.
3. Add the tool's registry entry, parser, and result-to-presentation adapter.
   Reuse a table or chart pattern, or supply a focused custom renderer.
4. Add focused data and interaction checks, plus a desktop/mobile visual review.
5. Add a contextual link from a dedicated page only when it is particularly
   useful. The catalog and tool switcher already expose the tool automatically.

Success means no bespoke header edits, new top-level navigation, duplicated
URL-state machinery, repeated tooltip implementation, or rewritten catalog
layout. A genuinely new chart interaction still costs design work; reuse cannot
eliminate the need to define a metric or obtain missing data. Measure ease of
extension by the seams touched and duplicated code avoided, not a promise that
all future ideas will take a fixed number of hours.

Before shipping the initial structure, add a throwaway fourth tool backed by
synthetic observations, using an existing renderer and one different input.
Confirm it appears in the catalog and switcher, accepts a direct URL, handles
an unsupported scope, and works on mobile without changes to existing tools.
Remove it before release. Also sketch a custom list or trajectory inside the
workspace to check that the layout does not assume every tool is the first
paired-dot chart. This is a proof of extension, not a request to ship those
features or invent a reusable abstraction for each hypothetical idea.

## Discovery as the collection grows

With three tools, show the available choices directly in a compact visual
catalog and a labeled switcher inside each tool. Descriptions explain the
analysis: “Results and expected values for every team,” rather than a chart
library term such as “Dumbbell.” Keep current-season tools as easy to find as
cross-season ones. Entering the catalog from a selected season retains that
context for tools that support it; the catalog is not globally filtered to
exclude tools that work across seasons.

When the catalog becomes difficult to scan, introduce small task-oriented
groups based on the actual tools, for example Teams, League, and Matches.
Do not encode History / Current as separate product areas. Add search only
when the real collection needs it. On desktop, a grouped switcher can preserve
fast changes without a permanent sidebar consuming chart space. On mobile,
the same labeled picker lists titles and short descriptions. Do not turn every
addition into another horizontal tab in the application header.

Each tool always opens useful data without configuration when the data allows
it. It may have a meaningful empty state when it requires a team or pair of
teams; do not select arbitrary identities invisibly. Saved favorites, user
accounts, dashboards, and authoring tools are outside this plan.

## State: encode an analysis, not an incidental hover

The URL contains the tool and its meaningful inputs, such as season, measure,
units, team IDs, range, and table ordering when supported. Stable IDs should
not depend on team display names. All generated links remain proxy-relative.
The tool resolver owns whether a change is compatible and makes scope changes
visible in its heading and controls.

There is no shared pinned-season or pinned-team requirement. Hover/tap/focus
inspection is local and temporary. A tool that lets the user explicitly choose
two teams to compare will encode those teams, because they define the analysis.
That is different from encoding whichever mark was touched most recently.

Changing an input updates the main surface in place, preserving useful focus
and scroll position. Deliberate analysis changes become browser history entries;
incidental inspection does not. Back/Forward restores the visible controls and
result together. A shareable link shows current cached data; it is not a frozen
historical snapshot. An incompatible scope must be explained or rejected,
rather than silently replaced with unrelated data.

## Examples that stress-test the structure

These examples demonstrate how later ideas could fit. They are candidates,
not milestones or required launch gates.

| Example idea | Likely extension | Additional questions or evidence |
| --- | --- | --- |
| This season's goals, GD, or points versus their expected values | Settings in Team performance, established in the first release | Valid complete metric coverage, matching denominators, active and uneven samples |
| Same analysis for one past season | Existing season input | Season-specific historical names; explain any missing results |
| Biggest positive/negative gaps within one season | Gap ordering or a diverging presentation in Team performance | Rank unrounded eligible values; missing pairs cannot silently win an extremum |
| Shield winners or last-place teams across seasons | Cross-season team comparison, potentially with named group presets | Verified official identity/order and complete selected records |
| Largest gaps across the whole archive | Cross-season comparison with an explicit eligible population and ranking | Ties, per-match versus totals, missing contenders, final versus active seasons |
| One team's performance across seasons | Team scope in a comparison tool if the structure fits | Historical names and reviewed continuity across rebrands/relocations |
| Progression through the same number of matches | A progression tool with match number as its axis | Cutoff, chronological order, comparator population, coverage over the selected prefix |
| Home advantage or scoring parity | A league tool or a related measure setting if the task remains coherent | A clearly defined measure and comparable in-progress sample |
| Match records or head-to-head results | A match-level tool with a useful sortable list | Match identity, club continuity, meaningful ordering, supported stages |

Do not front-load all these prerequisites. Historical final order is necessary
for an official Shield-winner group, not for a current team xG comparison.
Club continuity is necessary for a combined club history, not for a single
season's rows. Each idea should carry its own data work and acceptance criteria.

## Data rules that remain shared

Read SQLite only during page requests. Source refresh and maintenance remain
owned by the scheduler and sync command. Calculate each view from coherent
inputs and keep expected-value and fixture capabilities independent. Charts
and tables of the same analysis must use the same values and population.

Use raw results and expected values from the same selected matches. Require
complete xG for an xG aggregate and complete xPoints for an xPoints aggregate;
one does not establish the other. Missing values are not zero. Keep actual
results available when expected data is absent. Never infer expected points
from xG or conflate ASA retrospective expected points with Forecast Lab.

For current-season Team performance, show matches played and offer per-match
values by default. An active season is legitimate data, not an unfinished
historical record waiting to be excluded. Completed historical extrema require
stronger evidence than descriptive current-season rates. Eligibility belongs
to the analysis, not a universal Data explorer sample threshold.

Official standings identities and analytical sorting are separate. If a later
tool uses Shield winners or last place, verify official historical outcomes,
including applicable tiebreaks and points adjustments; do not use today's
ranking code to infer them. Ordinary earned points are `3 × wins + draws`;
keep sanctions separate so they do not masquerade as performance relative to
shot-based expected points. Historical labels must use the name for the season.

For any claimed maximum/minimum, define the eligible population and handle
missing contenders honestly. A covered runner-up is not a substitute for a
winner whose expected values are missing. Define ties before display rounding
and use one documented rule in chart and table. Avoid “all-time” claims for
the limited archive. These requirements should yield concise consequential
notes in the product, not an inventory or completeness dashboard.

## Next work after the initial release

First, assess whether people can discover the tools, inspect values, change
season/measure, and return to their dedicated season page. Fix navigation or
interaction failures before increasing the catalog. The initial current-season
comparison and league tools are the evidence that the shared structure works.

Then choose **one** new analytical idea with the owner based on interest and
available data. Use the extension workflow above, recording what could be
reused and what genuinely needed new design. If that addition requires changing
the global navigation or copying common interaction code, improve that seam
before repeating the pattern. Do not automatically execute the example table
as a feature queue.

Keep plans for future tools short and concrete: task, inputs, default view,
controls, data prerequisites, and checks. Use rendered desktop/mobile examples
when an interaction is new. Do not reopen the whole product taxonomy unless
actual additions demonstrate that the current model is failing.

Apply the repository's required checks to implementation, with focused tests
for new calculations and state transitions and visual review of the delivered
surface. Preserve unrelated work and update the active guide to behavior that
actually shipped. No forecast model changes, production backfills, framework
migration, or materialized aggregate cache is implied by this roadmap.
