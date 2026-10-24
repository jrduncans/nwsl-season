# How clinching works

This is the guide to the **current** qualification and clinching-scenario
implementation. It is deliberately layered: start with the short version, then
follow the proof flow or the implementation details as needed.

## The short version

For any season outcome with a top-`K` place threshold, a team has **clinched** only when
there is no feasible way for `K` other teams to finish ahead of it. The system
is deliberately conservative:

- `clinched` means the guarantee has been proved.
- `not_clinched` means the solver found a feasible completion that can keep
  the team below the threshold. It does **not** mean the team has been eliminated.
- `unresolved` means the system cannot honestly settle the answer from the
  available data, an outcome-only points proof, and its computation budget.

The clinching-scenarios page separately publishes a playoff-elimination scenario
when it has a stronger proof: eight opponents already have strictly more
points than the target can reach even by winning every remaining match. This
is not inferred from `not_clinched`, standings position, or a tiebreak.

The current 2026 regular-season rules are configuration, not solver constants:
16 teams, 30 matches per team, with Shield (top 1), home playoff game (top 4), and
playoffs (top 8). A stronger guarantee implies each weaker one: Shield →
home playoff game → playoffs. A team that secures a home playoff game normally
hosts the opening playoff match, but venue availability means hosting itself is not an unconditional
qualification guarantee.

```mermaid
flowchart TD
    A["Complete current match snapshot"] --> B["Qualification proof for every team and season outcome"]
    B --> C{"What can the points model prove?"}
    C -->|A blocking finish exists| D["not_clinched"]
    C -->|No K opponents can even tie| E["clinched"]
    C -->|Only a points tie can block| F["unresolved: score or disciplinary tiebreak"]
    E --> G["Persist current status and strongest standings badge"]
    D --> H["Calculate optional no-help winning path"]
    B --> I["Search next selected match slate"]
    I --> J["Publish only score-independent clinching conditions"]
    B --> K["With two or fewer matchdays left, search the rest of the season"]
    K --> L["Publish results with score conditions from the tiebreakers"]
```

The key idea is that the solver gives the target its **worst** remaining results
(losses) before evaluating its rivals. If the target is still guaranteed a
top-`K` finish from that pessimistic frontier, it is clinched.

## What must be true before a proof runs

Qualification is a derived batch, calculated after a durable match refresh;
ordinary page requests only read the persisted batch. Before any team is
evaluated, the refresher verifies:

- the match inventory has the expected teams and matches, and every
  participant has the configured number of matches;
- every match is either completed with both scores or `PreMatch` with neither
  score;
- every pending match has a valid kickoff time, so it can be put in a stable
  chronological order; and
- the configured season rules are valid.

If any check fails, every status is `unresolved` with
`incomplete_schedule`; the site does not quietly substitute a partial-table
answer. A shared qualification budget (five seconds by default) likewise
produces an explicit `compute_budget` result rather than a guess.

```mermaid
flowchart TD
    A["Successful match refresh"] --> B{"Complete and safe schedule?"}
    B -->|No| C["Persist unresolved rows: incomplete schedule"]
    B -->|Yes| D["Build one validated Evaluator"]
    D --> E["Evaluate every team x season outcome"]
    E --> F["Apply stronger-to-weaker implications"]
    F --> G["For not-clinched rows, calculate no-help paths"]
    G --> H["Persist one snapshot- and rules-versioned batch"]
    H --> I["Scenario batch reads the matching qualification batch"]
    I --> J["HTTP pages read the current persisted results"]
```

The result is tied to both the immutable match snapshot and the season-rule
version. This prevents a badge or scenario from being mixed with results
calculated from a different schedule or playoff format. When no scenario batch
matches the current snapshot, rules and definition version, the clinching page
says "Recalculation pending." instead of showing an older result. The standings table
shows a green badge for proved clinches and a red × marker (with a standings
key) only for a separately proved, points-only playoff elimination;
`not_clinched` alone never produces an elimination indicator.

## Status proof: the mental model

Let `F` be the target team's final points after:

1. applying completed scores and any caller-supplied fixed outcomes; and
2. making the target lose every remaining match that has not been fixed.

All remaining matches not involving the target are then the solver's
decisions. This is valid because any better target outcome can only make a
clinching proof easier. The solver asks two related questions about the target's
pessimistic frontier `F`:

- Can at least `K` opponents finish with **more than `F`** points?
- If not, can at least `K` opponents finish with **at least `F`** points?

```mermaid
flowchart TD
    A["Prepare pessimistic target frontier F"] --> B{"K opponents already have more than F?"}
    B -->|Yes| C["not_clinched: cheap bound"]
    B -->|No| D{"Fewer than K opponents can individually reach F?"}
    D -->|Yes| E["clinched: cheap bound"]
    D -->|No| F["Try a fast complete strict-points witness"]
    F -->|Found K above F| G["not_clinched: points optimization"]
    F -->|Not found| H["Exact search: can K opponents reach F plus 1?"]
    H -->|Yes| I["not_clinched: points optimization"]
    H -->|No| J["Exact search: can K opponents reach F?"]
    J -->|No| K["clinched: points optimization"]
    J -->|Yes| L["unresolved: a tie may require score tiebreaks"]
```

### Why two thresholds matter

The strict query, `F + 1`, identifies a points-only way to deny the target.
One feasible assignment is enough to prove `not_clinched`.

If no `K` opponents can finish strictly above `F`, the target cannot be
denied on points alone. The level query, `F`, distinguishes the remaining
cases:

| Result of the level query | Meaning |
| --- | --- |
| Fewer than `K` opponents can reach `F` | Even a tiebreak cannot put `K` teams ahead; the team is `clinched`. |
| `K` opponents can reach `F` | A score-based or unavailable tiebreak might matter, so the status is `unresolved`, not a premature clinch. |

This is why a team may look safe in the table but not receive a clinched badge:
the system refuses to turn an unproved goal-difference, head-to-head, or
disciplinary tiebreak outcome into a mathematical claim.

## How the coupled-match solver proves the points questions

Individual maximum points are a useful screen, but they are not a proof: two
rivals playing each other cannot both receive three points from that match. The
exact solver preserves that coupling.

For each threshold and candidate set of `K` opponents, it awards selected
opponents a win against unselected opponents, gives unrelated matches a draw,
and leaves only selected-versus-selected matches as decisions. It then searches
the remaining match graph for enough points to cover each selected team's
deficit to the threshold.

```mermaid
flowchart TD
    A["Choose a threshold and K opponents"] --> B["Give selected teams safe wins vs unselected teams"]
    B --> C["Keep selected-versus-selected matches as coupled decisions"]
    C --> D["Compute each selected team's points deficit"]
    D --> E{"Can any team cover its deficit with remaining matches?"}
    E -->|No| F["Prune this candidate set"]
    E -->|Yes| G{"Can each disconnected component cover its local deficits?"}
    G -->|No| F
    G -->|Yes| H{"Can all remaining matches supply the total deficit?"}
    H -->|No| F
    H -->|Yes| I["Try home win, away win, and draw"]
    I --> J{"Every selected deficit covered?"}
    J -->|Yes| K["Return and independently verify a full witness"]
    J -->|No| L["Memoize failed deficit state and continue"]
```

The solver uses branch-and-bound with memoized deficit states. It prunes when a
team, a disconnected component, or the remaining matches collectively cannot
supply enough points. A returned blocking witness assigns every remaining match;
canonical scorelines (`1-0`, `0-0`, `0-1`) make the witness readable
without pretending those scores affected a strict-points proof.

## Tiebreaks and the completed-season exception

While matches remain, the live proof uses points only. A level-points blocking
completion is reported as `unproved_score_tiebreak`; it is not simulated using
an arbitrary score cap.

When there are no pending matches, the system calculates the official
total-points table from actual scores. Its available ordering is points, goal
difference, wins, goals scored, head-to-head points, and head-to-head goals
scored. If a final tie still reaches least disciplinary points, which is not in
the cache, the system compares the tied group's best and worst possible ranks
with each season outcome's place threshold. A tie wholly above or below the
threshold is decisive; only a tie that crosses the threshold is `unresolved` with
`missing_disciplinary_rule`. The deterministic display order used as a final
fallback is never used as an official clinching proof.

## Reading a stored qualification result

Every result carries the team, season outcome, top-`K` value, status, method,
reason, point-count evidence, optional witnesses, no-help scenario, and diagnostics.

| Method | Interpretation |
| --- | --- |
| `cheap_bound` | A linear points bound decided the answer without exact search. |
| `points_optimization` | The coupled-match solver found a strict blocker or proved neither strict nor level blockers can reach the threshold. |
| `accessible_tiebreak` | A completed season was settled from actual scores using the available official table rules. |
| `unproved_score_tiebreak` | A level-points completion exists, but future score tiebreaks were not proved. |
| `missing_disciplinary_rule` | A completed-table tie needs disciplinary points that are unavailable. |
| `compute_budget` | The shared calculation deadline expired. |
| `incomplete_schedule` | The match snapshot was incomplete, unsafe, or could not be ordered. |
| `implied_achievement` | A stronger clinch implies this weaker season outcome. |

`StrictlyAhead` and `AtLeastLevel` record either exact counts or safe
lower/upper bounds, depending on the proof path. `BlockingWitness` shows a
schedule that denies the target; `FrontierWitness` shows the level-points
completion that made the tiebreak question unresolved.

## No-help scenarios

For a `not_clinched` result, the batch also asks: *Can this team guarantee the
season outcome by winning its own next matches, regardless of every other result?*

The evaluator fixes all of the target's remaining matches as wins, in kickoff
order. If that is enough, it binary-searches the earliest successful prefix and
stores those match IDs. It does **not** claim that any arbitrary number of
wins is enough: the named matches, including their opponents, are part of the
proof.

| No-help state | Meaning |
| --- | --- |
| `guaranteed` | Winning the listed chronological prefix clinches the season outcome without outside help. |
| `impossible` | Even winning every remaining target match cannot guarantee it. |
| `unresolved` | The endpoint depended on an unresolved proof or the budget expired. |
| `not_applicable` | The team had already clinched. |

## Next-slate clinching scenarios

The scenario feature answers a different question: *Which upcoming results are
sufficient for a clinch by this slate's cutoff?* It never replaces the normal
season-long status proof.

The slate is explicit. The system uses the next matchday when its data is
reliable; otherwise it includes pending matches in the 120-hour window starting
with the next kickoff. Every usable slate is searched. The proof-directed walk
prunes branches as soon as it can certify a sufficient clinch or rule out a
possible one, and uses the independently configured scenario budget as its only
operational limit. When the budget expires, any scenarios already found remain
published because they are certified; the result also says that additional scenarios
may exist.

```mermaid
flowchart TD
    A["Matching persisted qualification baseline"] --> B{"Baseline status"}
    B -->|Clinched| C["already_clinched: do not search"]
    B -->|Data or compute unresolved| D["unresolved scenario with limitation"]
    B -->|Score-tiebreak unresolved| E["Define and validate next slate"]
    B -->|Not clinched| E["Define and validate next slate"]
    E --> F{"Slate usable?"}
    F -->|No| D
    F -->|Yes| G["Walk home-win / draw / away-win tree"]
    G --> H{"Partial outcomes already points-clinch?"}
    H -->|Yes| I["Record a sufficient partial clause and prune"]
    H -->|No| J{"Best remaining target outcomes still cannot clinch?"}
    J -->|Yes| K["Prune impossible branch"]
    J -->|No| L{"Leaf reached?"}
    L -->|No| G
    L -->|Yes| M["Run exact fixed-result status proof"]
    M --> N{"Score-independent points clinch?"}
    N -->|Yes| O["Mark certified assignment"]
    N -->|No, tie unresolved| P["Mark tiebreak-dependent assignment"]
    N -->|No, not clinched| K
    O --> Q["Minimize complete coverage, or retain certified discovered clauses"]
    P --> Q
    Q --> R["Publish clauses, or tiebreak-dependent / cannot-clinch state"]
```

Only `cheap_bound` and `points_optimization` clinches are publishable
outcome-only scenarios. A scenario is tested against **every** slate
assignment it represents, then generalized where all of its outcomes are
certified. That permits honest phrases such as “does not win” while withholding
conditions that depend on goal margin or unavailable disciplinary data.

| Scenario state | Meaning |
| --- | --- |
| `already_clinched` | The baseline already proved the season outcome. |
| `can_clinch` | At least one score-independent, sufficient outcome-only scenario was certified. |
| `cannot_clinch` | All complete slate outcomes were definitively non-clinching. |
| `tiebreak_dependent` | A possible clinch may depend on scores or unavailable tiebreak data; no outcome-only scenario is published. |
| `unresolved` | The baseline, slate data, or compute budget prevented a conclusive search. |

When a `can_clinch` result has tiebreak-dependent leaves or ends at its compute
budget, the visible scenarios remain valid but may not cover every theoretical
scenario. The UI says so instead of implying that the displayed conditions are
necessary.

### Scenario presentation

A line under the page title gives the slate's local date range and expands
to list its included matches; the list stays collapsed by default so the
results remain near the top. "Already clinched" boxes list each team once,
under its highest clinched outcome, since that outcome implies the lower ones. Each team that can clinch, or can be eliminated,
gets one card: the team's name heads it, and each outcome ("Can clinch a home
playoff game", "Can clinch the playoffs") is a section under its own badge,
ordered by outcome rank. Cards follow each team's highest-ranked outcome, then
standings position. Color carries meaning: the team's name sits on a tinted
band (green for clinching, red for elimination), each outcome badge is solid,
and each result box is tinted by the team's own result in that match: green for
a win (or win or draw), gray for a draw, amber for a loss (or draw or loss), and
neutral when it spans several matches. A box that needs no outside help says
"✓ Clinches" (or "Eliminated"); otherwise its conditions follow "Clinches if"
("Eliminated if"), each shown as a pill. Within an outcome, the page groups confirmed scenarios by the team's own results, in kickoff
order and from wins through draws to losses. Overlapping conditions such as
“win” and “win or draw” are split so the draw group includes exactly the
outside help certified for a draw. All own-result group headings and factored
requirements stay visible. Only groups whose summary is omitted for size offer
an expandable scenario list. Results
from one match with identical outside help share a heading, such as “draws or
loses.” Two-match result orderings share a heading only when their outside help
is identical; for example, “wins one match and loses the other” still names
the two specific matches. Two-match groups with identical outside help also
merge when together they are exactly every result worth at least some points
total, every ordering included: five "✓ Clinches" rows for everything but two
losses become one "earns at least 1 point from these two matches" heading.
For unusual slates with more than four relevant own matches, the page retains
the original, potentially overlapping own-result groups to bound expansion.

An outcome's "Every clinching scenario requires" note lists outside-match
conditions shared by all of its scenarios. It leaves out the team's own
results, which already head every result group, and is hidden when nothing
else remains.

Within a group, a visible factored summary puts common requirements once above
an “any of” list. The summary may contain overlapping alternatives when that
is shorter and clearer. A group with more than twelve summary scenarios instead
directs readers to its exact scenarios, avoiding a large nested list.
Every requirement shared by all alternatives, including a score condition,
appears once at that level; pulling out shared requirements adds no nesting.
When different results of one outside match appear in several scenarios, the
summary groups a repeated result before other shared conditions. If an earlier
scenario already covers one result of a broader condition, the summary removes
that redundant result. For Bay FC's draw scenarios, KC wins sit together;
the SEA-win alternatives then name a DEN win where applicable. Each rewrite
must preserve the certified outcome union. Alternatives with the same number
of conditions are ordered by their displayed result text, keeping repeated
results such as DEN wins adjacent regardless of match IDs.

Groups whose summary is omitted offer expandable, complete certified scenarios;
a full visible summary has no duplicate scenario disclosure.
Each exact scenario puts its requirements on one compact line, separated by `+`,
with a club crest and two- or three-letter team code; each scenario is an `OR`
alternative to the others. When alternatives for two matches of the same team
exactly equal a minimum points total, the page names both matches and that
total. This rewrite checks all nine outcome combinations and never infers an
uncertified combination from a points total. Both the summary and exact scenarios
preserve the union of stored certified scenarios, including partial results.
Repeated equivalent scenarios and stronger scenarios already covered by a weaker
one need not be displayed repeatedly.

The exact-scenario view subtracts partial overlap between outside-help scenarios before
combining results into points totals. Paths with fewer match requirements
take precedence, with a stable match-ID and outcome-mask ordering to break
ties. Each later scenario keeps only outcomes not already represented. For example,
if “SEA wins or draws at LA + UTA draws vs LA” is already shown, the additional
scenario “SEA wins or draws at LA + UTA wins or draws vs LA + SEA wins or draws at
DEN” narrows to require a UTA **win**. When two equal-length scenarios both include
the same pair of draws, that pair remains in only one alternative. Subtraction
can split a scenario into several alternatives; it never drops a unique outcome
or adds an uncertified one. Points compaction preserves this disjoint union.

The pairwise coverage pass is capped at 256 scenarios per group. Overlap
subtraction is capped at 256 scenarios, including intermediate expansion, and
65,536 subtraction operations. Larger inputs or an expansion/work overflow
retain the original certified alternatives, which can still overlap. These
limits keep presentation work bounded on ordinary cache-only page requests.

Every complete certified scenario is represented by the visible summary, or
available in its group's disclosure when that summary is omitted.
The visible summary has at most one shared-condition factoring level; the
exact-scenario view stays flat.
Unlisted matches are unrestricted. The full included schedule remains collapsed
below the results, with its date range above them. Incomplete-result notices
remain visible for both clinching and elimination scenarios. Ordinary page requests
read only cached results and do not rerun qualification or scenario discovery.
No-help winning scenarios are labeled “No-help scenario” because they can include matches
after the displayed slate. Inside a team's card the line is just the win count
("No-help scenario: 2 wins"), since the card heading and badge name the team and
outcome; it expands to list the matches.

Use `make test-clinching` for focused scenario-proof and presentation checks.
These checks also run within `make test` and CI, including exhaustive
outcome-equivalence, disjoint detailed alternatives, future-match elimination,
and summary equivalence; visually verify desktop and 390px layouts, expansion,
keyboard access, and team filtering when changing the presentation.

## Rest-of-season scenarios

Late in the season the page also answers questions such as "in which scenarios
does Portland get a home playoff game?" exactly. Once two or fewer matchdays are
left, a second batch covers **every** remaining regular-season match, so each
assignment of results is a completed season apart from scores. A points tie that
straddles a place threshold is then settled by the tiebreakers instead of being
left unpublished. The season outcomes, card layout and own-result grouping are the
same as for the next slate, but most groups state what each rival must be held to
instead of listing scenarios (see [Rival summary](#rival-summary)).

"Matchdays left" counts the distinct matchdays of the pending matches; if any
pending match lacks a matchday, it is the most matches any team has left.

### Scope and the view switch

| Matchdays left | Page |
| --- | --- |
| Three or more | Next-slate scenarios only, as before. |
| Two (penultimate matchweek) | A segmented control, **Next matchday \| Rest of season**, defaulting to Next matchday. The view is the `view` query value (`next-matchday` or `rest-of-season`). |
| One (decision day) | Rest of season only, with no control: the next slate is the rest of the season. Decision day means the two slates name the same matches. |

The playoff-elimination cards always come from the next-slate batch. There are no
"misses out" cards for the rest of the season, and no line claiming the list is
complete. When the rest-of-season batch for the current snapshot and rules is
missing, that view says "Recalculation pending."

### Tiebreak order and score conditions

The 2022+ order is points, goal difference, wins, goals scored, head-to-head
points and head-to-head goals scored; a tied subgroup continues to the next
criterion without restarting. With the results fixed, wins and head-to-head points
are known. Only goal difference, goals scored and head-to-head goals scored can
become **score conditions**.

Every score condition compares two teams level on points, one of them the team
whose scenario it is, and counts only the remaining matches:

- the **goal-difference gain** is the first team's goal difference in its
  remaining matches minus the other's in theirs (a match between them counts for
  both);
- the **goals-scored gain** is the same for goals scored; and
- the **head-to-head gain** is the first team's goals minus the other's in their
  remaining matches against each other.

A condition is a cascade: the gain must be at least a minimum, or exactly one
less and the next tiebreaker decides. Each result bounds its margin: a win is +1
or better, a draw exactly 0 with equal goals, and a loss −1 or worse. Many cases
need no score condition. For example, if Portland and Utah both draw while level
on +7, goal difference stays level and Utah's extra win decides it: a certain
miss.

### Head-to-head groups

Head-to-head points count only the teams level after goal difference, wins and
goals scored, so whether the team beats one rival on head-to-head can depend on
which other rivals are level too. The search therefore splits each rival that can
finish level with the team through goals scored into three states: the team
finishes ahead of it before head-to-head, it finishes ahead of the team, or the
two finish **level** and the rival joins the team's head-to-head group. For every
combination of states it ranks the group by head-to-head points among exactly
those teams, then a subgroup still level by head-to-head goals scored among that
subgroup, as `standings.Calculate` does. Rivals that can never be level with the
team are compared pairwise.

The results are joined back into conditions. A rival whose states all give the
same result needs no condition. When "ahead" and "level" give the same result,
one "ahead or level" condition covers both, which is the usual two-team case. A
rival in the "behind" state gets a condition written from its side ("UTA gains
1+ goal difference on POR"). The level state reads "POR finishes level with UTA
on goal difference and goals scored", followed by a head-to-head goals condition
when their remaining matches against each other decide it.

### Wording

The page writes each condition out, with the current gap in brackets. Every
level is a gain, worded the same way for each measure:

- "POR gains 1+ goal difference on UTA (level at +7 now)"
- "POR gains 6+ goal difference on KC (KC +5 now)"
- "UTA gains 6+ goal difference on KC (KC +5 now), or exactly 5 and scores at
  least 7 more goals (KC +6 now)"
- "POR gains 11+ goal difference on LA (LA +10 now), or exactly 10 and scores
  at least as many goals (level at 48 now)"

A non-positive minimum reads "loses no goal difference to" or "loses at most 2
goal difference to"; a zero gain before the next level reads "or none and…".
When the results make a larger gain impossible, the level reads "gains exactly
2…" or "keeps the same goal difference gap to", or "finishes level with" when
that gap leaves the two level. When two teams' only remaining matches are
against each other, every gain follows those margins, so the condition is a
single goal-difference threshold. When three or more teams are level on points,
the team must finish ahead of enough of them; each choice of rivals is a scenario
whose conditions are joined as separate pills (the "and" of the scenario).

Rest-of-season summaries are never replaced by "Several certified scenarios":
the scenarios are the whole answer, so every group's summary is written out (up
to 256 paths, the coverage-pass bound). Most groups are shown as a rival summary
instead (below), which is far shorter.

### Rival summary

Listing scenarios over every remaining match multiplies quickly: one of Gotham's
2026 Shield groups ran to about 45 lines of nested "any of" lists. The page
therefore restates each result one rival at a time, which a reader can hold in
their head:

> **If NJ/NY Gotham FC wins one match and loses the other** — Clinches if all of:
> - SD takes 3 points or fewer (vs BOS and at BAY), or takes 4 points if NJY
>   gains 1+ goal difference on SD (level at +13 now)
> - WAS doesn't win both matches (at KC and vs CHI), or wins both matches if NJY
>   gains 2+ goal difference on WAS (WAS +1 now)

This is exact, not a simplification, because of two facts:

- **One rival at a time.** With the team's own results fixed, whether it
  finishes ahead of a rival depends only on the two teams' results and scores:
  points, goal difference, wins and goals scored are each team's own, and
  head-to-head between just the two follows from their matches. The team clinches
  a top-`K` place when at most `K − 1` teams finish ahead of it, so each row
  says how many of the rivals in doubt it must finish ahead of ("all of", "any
  of", "at least 2 of"). Other teams finish ahead of it, or behind it, whatever
  happens, and are not listed.
- **Points name the record.** A rival finishes behind on points below some total
  and ahead above it; only the total that leaves the two level needs a score
  condition. With one or two matches left, a points total also fixes the
  rival's wins, so "takes 4 points" is a single record. With three or more, a
  level total names its wins ("takes 3 points with 1 win").

The search builds the summary after it finishes, for each `can_clinch` result:
it fixes each combination of the team's own results (at most four matches), and
for every other team walks that team's remaining results (at most six matches),
comparing the two as the leaf search does. A rival that could finish level
through goals scored forms a head-to-head group with the team alone, ranked by
head-to-head points and then head-to-head goals scored.

That last step is the one exception. When **two** rivals could each finish level
with the team through goals scored, head-to-head among three teams could decide,
and a pairwise statement could be wrong either way. Such a row is still stated
when it clinches even if every rival in doubt finishes ahead, or cannot clinch
even if every rival that could be level finishes behind; otherwise it is
**withheld**, and the page shows that row's clauses as before (pinning the
team's own results, so they group as exactly that row). A result with every row
withheld, or a search that hit its budget, has no summary and shows only clauses.
The multiway-tie note appears only when the clauses are shown for some row.

Those fallback clauses can still contain independent requirements. Instead of
repeating every combination, their summary separates them into bulleted groups
under **Clinches if all of**. Common results and their score conditions stay
together: "KC loses both matches … and UTA gains …". Each group can contain its
own alternatives. For example, KC's two-loss case separates Angel City's record
from the coupled Seattle/Utah tiebreak. Utah's loss at Gotham separates Angel
City's record from Seattle's result and score condition. Neither case needs to
repeat the combinations of those independent requirements.
The selected view explicitly enables this grouping; the summary path limit
controls only when a summary is omitted for size.

This presentation rewrite checks that the distinct projections' counts multiply
back to the original number of clauses: every combination must already occur
in the certified clauses. Score predicates remain unchanged, so a multiway tie
is never silently replaced by pairwise comparisons. A one-rival group becomes
a points limit and optional score condition only after checking every result
of its one or two named matches, including both orderings of a draw and loss.
If a combination is missing or its score conditions differ, the original
alternatives remain. The pass applies only within the existing 256-path summary
bound. A complete grouped summary replaces the duplicate exact-scenario
disclosure; the disclosure remains available when a summary is omitted for size.

Rows merge under one heading as clause groups do ("wins one match and loses the
other", "earns at least 1 point from these two matches") when their rivals'
needs are identical. Rivals are listed in standings order. A row that needs no
help shows "✓ Clinches".

Inside the named team card, rest-of-season result headings use team codes to
avoid repeating full club names. Each rival's score-dependent alternative starts
on a new line. Groups already show no-help paths, so the rest-of-season view
omits the repeated no-help footer; a single group also omits the redundant
"Every clinching scenario requires" list. Separate no-help cards remain when
the search has not published a scenario group.
Already-clinched and already-eliminated teams sit in one collapsed disclosure
in this view, keeping the remaining scenarios near the top of the page.

### What is never claimed

- **Disciplinary points.** Teams identical through head-to-head goals scored would
  next be split on disciplinary points, which ASA doesn't provide. That case is
  never claimed. When it is reachable, the outcome notes that disciplinary points
  may decide.
- **Head-to-head goals among three or more who still play each other.** Three or
  more teams level through head-to-head points go to head-to-head goals among
  them. Those are known from completed matches unless two of them still play each
  other; then the outcome claims only what holds whichever way that goes, and
  notes that scenarios where three or more teams finish level on points, goal
  difference, wins, goals scored and head-to-head points, and still play each
  other, are not listed. The same note covers a leaf with more than six rivals
  that could be level, or more than 64 ways to choose rivals to finish ahead of.
- **Compute budget.** The existing "Scenario search was incomplete." note
  applies when the scenario budget runs out; published scenarios remain valid.

### How the search works

The search fixes results one match at a time: the team's own matches first, then
those of teams closest to it on points. A team whose points range lies wholly above
or below the team's cannot change its place, so a match between two such teams is
left unconstrained. Once every relevant match is fixed, the team's points are
final and every level rival has played out its season; each level rival is
compared in a small separate problem over the two teams' remaining matches, with
the head-to-head states described above.
Simple count bounds settle subtrees early: enough teams certainly ahead is a miss,
and too few teams possibly ahead is a clinch.

Outcome cubes merge only when they share the same score conditions and proof
method, so a score condition stays tied to the results it was derived from. The
cubes the search emits are disjoint, and merges keep them disjoint.

On the 2026 data with 13 matches left (3¹³ ≈ 1.6 million result assignments),
the whole rest-of-season batch takes a few milliseconds.

### Proof and storage

Tests build random small leagues and complete the remaining matches with every
scoreline up to a goal limit. For each one they compare every published scenario
with the official table from `standings.Calculate`: a scenario must only describe
scorelines where the team definitely finishes inside the threshold, and when no
limitation is stated, every such scoreline must be described. A rival summary
must describe exactly the scorelines where the team definitely finishes inside
the threshold, deferring to the clauses only in withheld rows. A scoreline where
disciplinary points decide must carry the disciplinary note.

The rest-of-season batch is stored beside the next-slate batch with its own
definition version (`rest-of-season-v4`; the next slate is `next-slate-v6`), its
slate source `rest_of_season`, and a `disciplinary_possible` flag per result.
Clauses carry their score conditions in `clauses_json`; the rival summary is
`rivals_json` (`null` when there is none).

## Playoff-elimination scenarios

Alongside positive clinching opportunities, the page can show that a team
**can be eliminated from the playoffs** during the same slate. The search uses
the target's best possible points ceiling: every still-unfixed target match is
treated as a win. It first counts opponents already strictly above that
ceiling. When that alone does not prove elimination, it asks whether any legal
completion of the other remaining matches can keep at least eight opponents
at or below the ceiling. If no such completion exists, future head-to-head
matches force an eighth opponent above it and the slate condition is certified.

For a fixed candidate set of opponents to keep below the ceiling, a match
against anyone outside the set can award the outsider the win. Only matches
between two selected opponents constrain their points capacities. The exact
search checks every legal result of those matches, with the scenario budget as
its compute limit. A found feasible completion is a reason to withhold the
elimination claim; an exhausted search never becomes a proof.

The initial, already-above-ceiling check is completed before any budget-limited
scenario search, so a team eliminated by that cheap bound remains identified
even if earlier team searches consume the shared scenario budget. A
future-match proof uses the available compute budget and can be retried.

This makes the claim independent of every score tiebreak. The feature still
withholds close cases in which a team could be eliminated only through goal
difference or unavailable tiebreak data. A team satisfying either points-only
proof before the slate is displayed as already eliminated. Changing the proof
semantics increments the scenario definition version so the persisted batch is
recalculated.

## Where to look in the code

- [`internal/clinching/evaluate.go`](../internal/clinching/evaluate.go)
  contains the status decision flow.
- [`internal/clinching/cutoff.go`](../internal/clinching/cutoff.go)
  implements the exact coupled-match threshold solver.
- [`internal/clinching/evaluator.go`](../internal/clinching/evaluator.go)
  reuses a validated snapshot, accepts fixed outcomes, and derives no-help scenarios.
- [`internal/qualification/refresher.go`](../internal/qualification/refresher.go)
  validates, batches, implies weaker season outcomes, and persists status.
- [`internal/scenarios/search.go`](../internal/scenarios/search.go) traverses
  the next slate; [`coverage_minimize.go`](../internal/scenarios/coverage_minimize.go)
  turns certified assignments into human-sized scenarios.
- [`internal/scenarios/season.go`](../internal/scenarios/season.go) searches the
  rest of the season, and
  [`season_tiebreak.go`](../internal/scenarios/season_tiebreak.go) derives score
  conditions for one level rival;
  [`season_rivals.go`](../internal/scenarios/season_rivals.go) builds the rival
  summary, and [`clinching_rivals.go`](../internal/app/clinching_rivals.go)
  words it.
- [`internal/scenariorefresh/refresher.go`](../internal/scenariorefresh/refresher.go)
  stores scenarios only against the matching qualification snapshot.

For behavior-level examples and guardrails, see the tests in
[`internal/clinching`](../internal/clinching) and
[`internal/scenarios`](../internal/scenarios).
