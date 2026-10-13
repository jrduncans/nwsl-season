# Historical outcome exceptions

Reviewed 2026-10-13. Explore derives normal season records from cached ASA
regular-season fixtures and uses postseason fixtures plus the existing
competition catalog to establish qualification, hosting, byes and seeds.
There is no manually maintained table of season totals or official standings.

The small source-controlled
[exception file](../internal/competition/outcome_overrides.json) supplies only
facts that cannot be recovered from those fixtures. It does not fetch its source
URLs during synchronization or page rendering.

## Season-specific tiebreakers

| Regular seasons | Computable criteria in order | First unavailable criterion |
| --- | --- | --- |
| 2013–2019, 2021 | Head-to-head win–loss (PPG for 3+ clubs), overall GD, overall GF; repeat for away games, then home games | Coin toss / drawing of lots |
| 2022–2026 | Overall GD, wins, GF, head-to-head points, head-to-head GF | Disciplinary points |

The shared standings policy implements these eras rather than copying individual
season rankings. Earlier multi-club ties restart the two-club procedure when
only two remain; modern subgroups continue with the next criterion.

Evidence includes the league's
[2017 full procedure](https://www.nwslsoccer.com/news/playoff-picture-courage-clinch-week-18),
[2018 procedure](https://www.nwslsoccer.com/news/playoff-picture-reign-close-to-clinching-a-playoff-spot),
the supplied [2019 season rules](https://en.wikipedia.org/wiki/2019_National_Women%27s_Soccer_League_season#Tiebreakers),
and [2021 season rules](https://en.wikipedia.org/wiki/2021_National_Women%27s_Soccer_League_season#Tiebreakers).
The league's [2022 procedure](https://www.nwslsoccer.com/news/nwsl-playoff-scenarios)
explicitly describes continuing to the next criterion after a subgroup remains.
The [2023 playoff guide](https://www.nwslsoccer.com/news/what-to-know-about-2023-nwsl-playoffs),
[2024 scenarios](https://www.nwslsoccer.com/news/nwsl-decision-weekend-playoff-scenarios),
and [2025 competition rules](https://images.nwslsoccer.com/image/private/t_q-good/prd/kqbdfpjgywmisiyhhtnp.pdf)
corroborate the modern policy, also used by the existing 2026 standings.
The app's ASA archive starts in 2016; earlier policy years are supported by the
shared algorithm but have no threshold datapoints until fixtures and catalog
formats exist. No 2020 regular season existed.

Washington and Utah's 2019 34-point tie now resolves from cached head-to-head
results, placing Washington fifth. No finishing-order override is needed.
Disciplinary records and random draws are unavailable in ASA; a remaining tie
needs source-backed evidence only if playoff fixtures cannot resolve it.

## 2021: two awarded matches missing from ASA

Washington forfeited one match to OL Reign and one to Portland. The ASA archive
contains 118 played matches; the two awards make the official record 120.
The league announcements establish
[the OL Reign award](https://www.nwslsoccer.com/news/washington-spirit-ol-reign-canceled)
and [the Portland award](https://www.nwslsoccer.com/news/nwsl-announces-fines-to-washington-spirit).
The exception names each winner and loser, the home team and awarded 3–0 score,
without duplicating season records. Portland hosted the September 4 fixture
([match preview](https://www.nwslsoccer.com/news/match-preview-portland-thorns-fc-vs-washington-spirit));
Washington hosted the September 12 fixture
([club announcement](https://washingtonspirit.com/blog/2021/09/11/washington-spirit-ol-reign-match-canceled/)).
Scores feed wins, goal difference and head-to-head/venue criteria as well as PPG.

Derived records become Portland 44 points / 24 games, OL Reign 42 / 24 and
Washington 39 / 24. Official PPG uses those totals. Played-match records remain
41 / 23, 39 / 23 and 39 / 22 respectively and appear in record notes.
Awards fill deficits relative to the observed full-season game count. An award
already included by ASA is skipped; inconsistent residual counts withhold the
season. Awards never mutate the cache or other Explore views.

## 2024: Angel City's three-point deduction

The league's
[disciplinary announcement](https://www.nwslsoccer.com/news/nwsl-announces-disciplinary-action-against-angel-city-fc-for-salary-cap-violation)
establishes a three-point deduction. Apply it to the ASA-derived earned total
for Angel City's explicit identity. No team total is copied into the exception.
This changes 27 earned points to 24 official points. Angel City is below the
published cutoff boundaries, but the adjustment is applied before constructing
all possible ranks and the season notes cite the decision.

## Derivation and maintenance

2022's home-game tie and 2023's playoff tie resolve from the applicable
season's match criteria and are corroborated by postseason participants and
hosts. Alphabetical display order and PPG sorting never certify outcome ranks.
The exception file currently contains no finishing-order overrides.

A normal new season enters through the existing ASA synchronization and
competition catalog, after regular-season closure and a complete resolved
postseason. No new outcome record or audit entry is required. Add an exception
only when an award, deduction, venue exception or unresolved tied order needs
facts absent from ASA. A venue exception conflicting with inferred hosting must
remain withheld until the inference can represent that fact; physical venue
alone is not proof of seed entitlement.

Known incomplete inventory, nonterminal games, unresolved postseason results,
unexplained unequal game counts withhold the whole season. Conflicting seed
evidence and an ambiguous boundary withhold only the affected outcomes. Historical seasons
without verified regular-season inventory use balanced game counts and completed
postseason evidence; these checks cannot detect every uniformly missing set of
regular-season fixtures. They must not be described as an independent fixture
audit. Team display names follow the cached ASA identities, consistently with
other Explore views.
