# Opponent adjustment rescales match values with the default model's factors

Match by match's **Adjust for opponents** rescales each match's goals and xG
to an average opponent at a neutral venue with even rest
([#192](https://github.com/jrduncans/nwsl-season/issues/192)). We decided:

- **Multiplicative, not additive.** Values are divided by factors: a discount
  against weak opponents and a premium against strong ones, never a signed
  gap. Values stay in goals and xG, and the adjustment matches how the
  forecast model combines attack, defense and venue. Zero stays zero, so a
  goalless match or clean sheet earns no credit for the opponent's strength;
  we accept that, and rolling averages soften it.
- **The default forecast model as written.** Factors come from
  xg-poisson-schedule-load-v1: shrunk xG ratings, league venue rates from the
  current and two previous regular seasons, the schedule-load shift and the
  rate clamp, with an average team in the judged team's place. We reuse one
  validated model rather than invent a second rating system, and a test
  checks the factors against the fitted model.
- **Hindsight ratings.** Each opponent is rated on every completed match of the
  season, not only those before kickoff. Early-season ratings would be mostly
  prior and the adjustment would say little; the cost is that earlier bars can
  shift slightly as the season goes on.
- **Leave-one-out.** The judged match is left out of the ratings and the
  league venue rates, so a dominant performance cannot raise its own baseline,
  and changing a match's xG never changes its own factors.
- **One set of factors for both scoring bases.** Factors always use full xG,
  as the model does; excluding penalties changes only the values divided
  (non-penalty goals and npxG). A second, npxG-based rating would make the
  same opponent stronger or weaker depending on a display choice.

Points stay official. Opponent-adjusted points are a separate question
([#193](https://github.com/jrduncans/nwsl-season/issues/193)).
