package forecast

import (
	"fmt"
	"math"

	"github.com/jrduncans/nwsl-season/internal/scheduleload"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

// TeamFactors rescale one team's values in one match to an average opponent
// at a neutral venue with even rest: divide the team's goals and xG by Scored,
// and the goals and xG it allowed by Allowed. ExpectedPoints is an average
// team's expected points in the team's place; subtracting its difference from
// the season's AveragePoints from points and xPoints shifts them the same way.
type TeamFactors struct {
	Scored, Allowed, ExpectedPoints float64
}

// FixtureFactors are both teams' factors for one completed fixture.
type FixtureFactors struct {
	Home, Away TeamFactors
}

// OpponentAdjustment holds the factors for every completed fixture.
type OpponentAdjustment struct {
	Fixtures map[string]FixtureFactors
	// AveragePoints is the mean ExpectedPoints over every team's completed
	// fixtures: an average team's expected points in the season's average match.
	AveragePoints float64
	// XGMatches of the CompletedMatches have xG, so contribute to ratings.
	XGMatches, CompletedMatches int
}

// OpponentAdjustments returns modelFactors normalized so that, across every
// team's completed fixtures, the season's scored factors average one, as do
// its allowed factors, and averages their expected points. The average opponent is then this season's: the model's
// league rate pools two previous seasons, so a higher- or lower-scoring season
// would otherwise shift every team's adjusted values alike.
func OpponentAdjustments(input FitInput) (OpponentAdjustment, error) {
	adjustment, err := modelFactors(input)
	if err != nil || len(adjustment.Fixtures) == 0 {
		return adjustment, err
	}
	// Sum in fixture order: map order would vary the last bits between calls.
	var scored, allowed, points float64
	for _, game := range input.Games {
		if fixture, ok := adjustment.Fixtures[game.ID]; ok && game.Status == standings.CompletedStatus {
			scored += fixture.Home.Scored + fixture.Away.Scored
			allowed += fixture.Home.Allowed + fixture.Away.Allowed
			points += fixture.Home.ExpectedPoints + fixture.Away.ExpectedPoints
		}
	}
	appearances := float64(2 * len(adjustment.Fixtures))
	scored, allowed = scored/appearances, allowed/appearances
	adjustment.AveragePoints = points / appearances
	normalize := func(factors TeamFactors) TeamFactors {
		return TeamFactors{Scored: factors.Scored / scored, Allowed: factors.Allowed / allowed, ExpectedPoints: factors.ExpectedPoints}
	}
	for id, fixture := range adjustment.Fixtures {
		adjustment.Fixtures[id] = FixtureFactors{Home: normalize(fixture.Home), Away: normalize(fixture.Away)}
	}
	return adjustment, nil
}

// modelFactors applies xg-poisson-schedule-load-v1 as written to each
// completed fixture, with an average team (attack = defense = 1) in place of
// the team being judged. A factor is that team's clamped expected rate at the
// fixture's venue and schedule load, divided by the league rate. Its expected
// points come from those clamped rates as independent Poisson scores.
//
// Ratings use every completed fixture in the season, in hindsight, except the
// one being judged: it is left out of the team ratings and the league venue
// rates, so a match never sets its own baseline. A fixture without a kickoff
// has no schedule-load term.
func modelFactors(input FitInput) (OpponentAdjustment, error) {
	known, err := validateTeams(input.Teams)
	if err != nil {
		return OpponentAdjustment{}, err
	}
	leagueGames, leagueXG, historical := twoSeasonXGVenueSample(input)
	validated := func(game standings.Game, xgoals map[string]ExpectedGoals) (ExpectedGoals, bool, error) {
		xg, ok := xgoals[game.ID]
		if !ok || game.Status != standings.CompletedStatus {
			return xg, false, nil
		}
		if xg.GameID != "" && xg.GameID != game.ID {
			return xg, false, fmt.Errorf("xG game ID %q does not match fixture %q", xg.GameID, game.ID)
		}
		if !validXG(xg.Home) || !validXG(xg.Away) {
			return xg, false, fmt.Errorf("invalid xG for game %q", game.ID)
		}
		return xg, true, nil
	}
	matches, homeXG, awayXG := historical.XGMatches, historical.HomeXG, historical.AwayXG
	for _, game := range leagueGames {
		xg, ok, err := validated(game, leagueXG)
		if err != nil {
			return OpponentAdjustment{}, err
		}
		if ok {
			matches++
			homeXG += xg.Home
			awayXG += xg.Away
		}
	}
	totals := make(map[string]xgTotals, len(known))
	scheduled := make([]standings.Game, 0, len(input.Games))
	result := OpponentAdjustment{Fixtures: map[string]FixtureFactors{}}
	for _, game := range input.Games {
		if !game.Kickoff.IsZero() {
			scheduled = append(scheduled, game)
		}
		if game.Status != standings.CompletedStatus {
			continue
		}
		_, homeKnown := known[game.HomeTeamID]
		_, awayKnown := known[game.AwayTeamID]
		if !homeKnown || !awayKnown {
			return OpponentAdjustment{}, fmt.Errorf("completed game %q references an unknown team", game.ID)
		}
		result.CompletedMatches++
		xg, ok, err := validated(game, input.XGoals)
		if err != nil {
			return OpponentAdjustment{}, err
		}
		if !ok {
			continue
		}
		result.XGMatches++
		totals[game.HomeTeamID] = addXGTotals(totals[game.HomeTeamID], xg.Home, xg.Away, 1)
		totals[game.AwayTeamID] = addXGTotals(totals[game.AwayTeamID], xg.Away, xg.Home, 1)
	}
	loads, err := scheduleload.Calculate(scheduled)
	if err != nil {
		return OpponentAdjustment{}, err
	}
	for _, game := range input.Games {
		if game.Status != standings.CompletedStatus {
			continue
		}
		home, away := totals[game.HomeTeamID], totals[game.AwayTeamID]
		looMatches, looHome, looAway := matches, homeXG, awayXG
		if xg, ok, _ := validated(game, input.XGoals); ok {
			home = addXGTotals(home, -xg.Home, -xg.Away, -1)
			away = addXGTotals(away, -xg.Away, -xg.Home, -1)
			looMatches--
			looHome -= xg.Home
			looAway -= xg.Away
		}
		homeRate := (looHome + leaguePriorGames*priorHomeGoals) / (float64(looMatches) + leaguePriorGames)
		awayRate := (looAway + leaguePriorGames*priorAwayGoals) / (float64(looMatches) + leaguePriorGames)
		leagueRate := (homeRate + awayRate) / 2
		homeAttack, homeDefense := xgStrengths(home, leagueRate)
		awayAttack, awayDefense := xgStrengths(away, leagueRate)
		load := loads[game.ID]
		// The model's schedule-load shift: the home rate's multiplier, and the
		// away rate's divisor.
		shift := math.Exp((scheduleload.Congestion(load.Away) - scheduleload.Congestion(load.Home)) / 2)
		side := func(scored, allowed float64) TeamFactors {
			scored, allowed = clamp(scored), clamp(allowed)
			outcomes := poissonDistribution{homeRate: scored, awayRate: allowed}.Outcomes()
			return TeamFactors{Scored: scored / leagueRate, Allowed: allowed / leagueRate, ExpectedPoints: 3*outcomes.HomeWin + outcomes.Draw}
		}
		result.Fixtures[game.ID] = FixtureFactors{
			Home: side(homeRate*awayDefense*shift, awayRate*awayAttack/shift),
			Away: side(awayRate*homeDefense/shift, homeRate*homeAttack*shift),
		}
	}
	return result, nil
}

func addXGTotals(totals xgTotals, scored, allowed float64, played int) xgTotals {
	totals.played += played
	totals.forGoals += scored
	totals.against += allowed
	return totals
}
