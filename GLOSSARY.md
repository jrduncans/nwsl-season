# NWSL Season

The language of a site that tracks the current NWSL season and lets visitors
explore recorded results across the archive.

## Explore

**Explore view**:
One way of charting the archive in Explore, identified by its view key, such as
Match by match or Parity.
_Avoid_: tab, panel, page

**View group**:
One of the three families of Explore views, by scope: Compare teams (every team,
one season), Team profile (one team) and League trends (the league across
seasons).
_Avoid_: section, category

**Display**:
One of the alternative presentations within Compare teams, such as Outlier plot
or Table, which show the same population.
_Avoid_: sub-view, mode

**Scoring basis**:
Whether goal and xG figures include penalties or exclude them (non-penalty).
Official points and ASA xPoints always include penalties.
_Avoid_: penalty mode

**Points-only selection**:
An Explore selection whose plotted values and eligibility rules are all official
points or xPoints, so the scoring basis cannot change anything shown.

## Competitions

**Season**:
A year, holding every competition played in it.

**Competition type**:
A recurring kind of competition: Regular Season, Playoffs or Challenge Cup.
In conversation the bare name often means the type; this term is for when
precision matters.

**Competition**:
One season's instance of a competition type, such as the 2026 Regular Season or
the 2024 Playoffs.
_Avoid_: edition, season (for the regular season)

**Stage**:
A phase of a competition. Most competitions have exactly one; only the
2020–2023 Challenge Cups split into a group stage and a knockout round. Each
season page covers one stage.
_Avoid_: scope, season-stage

**Competition status**:
Whether a competition is Upcoming, In progress or Complete.
_Avoid_: season phase, active

## Matches

**Match**:
One contest between two teams, whether scheduled, played or abandoned.
_Avoid_: game, fixture

**Matchday**:
The league's numbered round of regular-season matches, as published by ASA.

## Season outcomes

**Season outcome**:
A top-of-table result a team can secure over a regular season: the Shield, a
home playoff game, a playoff bye or the playoffs.
_Avoid_: achievement, qualification (for the umbrella), target

**Shield**:
The season outcome for finishing first in the regular season.

**Home playoff game**:
The season outcome for earning a seed that hosts the team's first playoff match;
under current rules, the top four.
_Avoid_: top-four seed, top 4, home playoff seed

**Playoff bye**:
The season outcome for earning a seed that skips the opening playoff round, in
seasons that had byes.

**Playoffs**:
The season outcome for qualifying for the postseason, and the competition type
that follows the regular season.

**Place threshold**:
The lowest finishing place that secures a season outcome, such as eighth for
the playoffs. Bare "threshold" is fine when the measure is clear.
_Avoid_: cutoff, cut line

**Points threshold**:
The points or PPG of the team that finished at a past season's place threshold:
roughly what it took to secure that outcome that year.
_Avoid_: cutoff, cut line

**Playoff line**:
The line drawn in standings just below the playoffs' place threshold.

## Clinching

**Clinched**:
Proved to secure a season outcome whatever the remaining results.

**Not clinched**:
Some possible remaining results would leave the team without that season
outcome. It never means the team is eliminated.

**Unresolved**:
The site cannot honestly settle whether a team has clinched, because data is
missing, a tiebreak would decide it, or the proof ran out of time.

**Eliminated**:
Proved unable to secure a season outcome, because enough opponents already have
more points than the team can reach by winning every remaining match.
_Avoid_: inferring it from not clinched or from standings position

**Slate**:
The next batch of unplayed matches that clinching scenarios are calculated for;
usually the next matchday.
_Avoid_: round, week, matchday (as a synonym)

**Scenario**:
One combination of match results that on its own is enough for a team to clinch,
or be eliminated from, a season outcome. A team can have several.
_Avoid_: path, clause

**Rest of season**:
The slate of every remaining regular-season match, used once two or fewer
matchdays are left. Its scenarios can add score conditions.

**Score condition**:
A requirement on goals that a rest-of-season scenario adds when a team ends
level on points with a rival: a goal-difference gain over the remaining matches,
then goals scored or head-to-head goals scored if that gain is exactly level.

**Outside help**:
Results a scenario needs from matches the team is not playing in.

**No-help scenario**:
A run of the team's own wins, possibly beyond the slate, that clinches without
outside help.
_Avoid_: no-help path, season-long path

## Measures

**xPoints**:
ASA's retrospective expected points for one team in one match, from 0 to 3,
based on the quality of the shots in that match.
_Avoid_: expected points, xPts (except as a tight column header)

**xPPG**:
xPoints per match played.

**Schedule difficulty**:
How hard a team's remaining regular-season matches are, judged by its
opponents' records, venue and schedule load.
_Avoid_: strength of schedule, SOS

**Schedule load**:
The extra strain on a team in a match, combining two parts: short recovery time
since its previous match, and accumulated load from playing several matches in
a short span.
_Avoid_: congestion, fixture load

**Opponent adjustment**:
A team's goals, xG, points and xPoints in each played match, rescaled to what
they would be against an average opponent at a neutral venue with even rest,
using the default forecast model's opponent ratings. It describes matches already played;
Schedule difficulty describes remaining ones.
_Avoid_: strength of schedule, opponent-adjusted form

## Forecast Lab

**Projected points**:
A team's mean final points total across simulated seasons.
_Avoid_: expected points

**Forecast model**:
A named, versioned method for estimating the scores of remaining matches, such
as xG Poisson (schedule load). Plain "model" is fine in context.
_Avoid_: preset, score model

**Fixed outcome**:
A visitor's assumption that a specific match ends in a home win, draw or away
win, without fixing the score.
_Avoid_: fixed result, fixed scoreline
