package app

import (
	"context"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/clinching"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/scenarios"
	"github.com/jrduncans/nwsl-season/internal/simulation"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

// forecastCertainty records top-K finishes that are already settled for the
// real season. A settled finish stays settled under any forecast scenario,
// because fixing remaining results only narrows the possible outcomes. The
// forecast shows these as exact 100% or 0%; simulated near-certainties are
// shown as >99.9% or <0.1% instead.
type forecastCertainty struct {
	clinched   map[string]map[int]bool
	eliminated map[string]map[int]bool
}

func (c forecastCertainty) isClinched(teamID string, topK int) bool {
	return c.clinched[teamID][topK]
}

func (c forecastCertainty) isEliminated(teamID string, topK int) bool {
	return c.eliminated[teamID][topK]
}

func markPlace(values map[string]map[int]bool, teamID string, topK int) {
	if values[teamID] == nil {
		values[teamID] = map[int]bool{}
	}
	values[teamID][topK] = true
}

// forecastCertainties loads the same proofs that annotate the standings table
// and adds points-only eliminations for every forecast column. Nothing is
// marked unless the qualification run for this exact fixture snapshot
// completed, which also guarantees a complete fixture inventory.
func (a *application) forecastCertainties(ctx context.Context, data cache.SeasonData, scope requestCompetition, rules competition.Rules, verified bool, presentation seasonPresentation) forecastCertainty {
	certainty := forecastCertainty{clinched: map[string]map[int]bool{}, eliminated: map[string]map[int]bool{}}
	if !scope.qualificationAvailable() || !verified || presentation.Phase == seasonPhaseUpcoming || data.FixtureSnapshotID == "" || rules.Version == "" {
		return certainty
	}
	qualificationStore, ok := a.store.(interface {
		QualificationForSnapshot(context.Context, string, string) (cache.QualificationSnapshot, bool, error)
	})
	if !ok {
		return certainty
	}
	qualification, found, err := qualificationStore.QualificationForSnapshot(ctx, data.FixtureSnapshotID, rules.Version)
	if err != nil || !found || qualification.Run.Outcome != "complete" {
		return certainty
	}
	for _, status := range qualification.Statuses {
		if status.Status == clinching.Clinched {
			markPlace(certainty.clinched, status.TeamID, status.TopK)
		}
	}
	places := make([]int, 0, len(rules.Achievements))
	for _, achievement := range rules.Achievements {
		places = append(places, achievement.TopK)
	}
	for teamID, topKs := range pointsEliminations(data.Teams, standingsGames(data.Games), places) {
		for topK := range topKs {
			markPlace(certainty.eliminated, teamID, topK)
		}
	}
	if scenarioStore, ok := a.store.(interface {
		ScenarioForSnapshot(context.Context, string, string, string) (cache.ScenarioSnapshot, bool, error)
	}); ok {
		if snapshot, found, err := scenarioStore.ScenarioForSnapshot(ctx, data.FixtureSnapshotID, rules.Version, scenarios.DefinitionVersion); err == nil && found && snapshot.Run.Outcome == "complete" {
			for teamID := range eliminatedTeams(snapshot.Results) {
				markPlace(certainty.eliminated, teamID, playoffPlaces(rules))
			}
		}
	}
	return certainty
}

// pointsEliminations reports, for each team, the top-K places it can no longer
// reach: at least K other teams already have more points than the team can
// finish with. Every unfinished fixture counts as a possible win, so the bound
// never depends on tiebreakers or on how the remaining matches go.
func pointsEliminations(teams []standings.Team, games []standings.Game, places []int) map[string]map[int]bool {
	points := map[string]int{}
	for _, row := range standings.Calculate(teams, games, standings.OfficialTotalRules()) {
		points[row.Team.ID] = row.Record.Points
	}
	maximum := map[string]int{}
	for id, value := range points {
		maximum[id] = value
	}
	for _, game := range games {
		if game.Status == standings.CompletedStatus && game.HomeScore != nil && game.AwayScore != nil {
			continue
		}
		maximum[game.HomeTeamID] += 3
		maximum[game.AwayTeamID] += 3
	}
	eliminated := map[string]map[int]bool{}
	for _, team := range teams {
		ahead := 0
		for _, other := range teams {
			if other.ID != team.ID && points[other.ID] > maximum[team.ID] {
				ahead++
			}
		}
		for _, topK := range places {
			if ahead >= topK {
				markPlace(eliminated, team.ID, topK)
			}
		}
	}
	return eliminated
}

type forecastChanceText struct {
	Shield, TopFour, Playoff, Championship string
}

// forecastChances formats one team's forecast columns. Playoff elimination
// also rules out a top-four seed, the Shield, and the Championship.
func forecastChances(row simulation.TeamResult, playoffPlaces int, certainty forecastCertainty) forecastChanceText {
	out := certainty.isEliminated(row.Team.ID, playoffPlaces)
	return forecastChanceText{
		Shield:       forecastChance(row.ShieldProbability, certainty.isClinched(row.Team.ID, 1), out || certainty.isEliminated(row.Team.ID, 1)),
		TopFour:      forecastChance(row.TopFourProbability, certainty.isClinched(row.Team.ID, 4), out || certainty.isEliminated(row.Team.ID, 4)),
		Playoff:      forecastChance(row.PlayoffProbability, certainty.isClinched(row.Team.ID, playoffPlaces), out),
		Championship: forecastChance(row.ChampionshipProbability, false, out),
	}
}

// forecastChance formats a simulated probability, reserving exact 100% and 0%
// for finishes that are proven rather than merely simulated.
func forecastChance(value float64, clinched, eliminated bool) string {
	switch {
	case clinched:
		return "100%"
	case eliminated:
		return "0%"
	}
	return boundedPercent(value)
}

// boundedPercent keeps one-decimal rounding from presenting a possible but
// rare outcome as 0.0%, or an unsettled outcome as 100.0%.
func boundedPercent(value float64) string {
	switch {
	case value >= .9995:
		return ">99.9%"
	case value < .0005:
		return "<0.1%"
	}
	return percent(value)
}
