package app

import (
	"context"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/forecaststate"
	"github.com/jrduncans/nwsl-season/internal/simulation"
)

// The By match view answers "who do I want to win?" with what the marginal
// table cannot: it re-runs the forecast once per possible winner of each unplayed
// match with known participants, exactly as applying that scenario would.

type playoffImpactOutcome struct {
	Team       teamNameView
	MobileName string
	Advance    string
}

type playoffImpactRow struct {
	Team            teamNameView
	MobileName      string
	SeedText        string
	EliminationText string
	// Now and each of Cells hold one text per metric, in Metrics order.
	Now         []string
	Cells       [][]string
	Participant bool
}

type playoffImpactMatch struct {
	ID, Label string
	Fixture   string
	// Metrics names the columns under Now and each outcome. A result only
	// moves chances in rounds beyond the one the winner advances to, so the
	// list shrinks as the bracket does and ends with the title.
	Metrics  []string
	Outcomes []playoffImpactOutcome
	Rows     []playoffImpactRow
}

func playoffSeedLookup(postseason *simulation.Postseason) map[string]int {
	seeds := map[string]int{}
	for seed, id := range postseason.Seeds {
		seeds[id] = seed
	}
	return seeds
}

// playoffImpact returns nil when the forecast is withheld or a conditional run
// fails; the unconditional views never depend on it.
func (a *application) playoffImpact(ctx context.Context, data cache.SeasonData, state forecaststate.State, places int, postseason *simulation.Postseason, format competition.BracketFormat, eligible map[string]simulation.PostseasonMatch, base simulation.Result, rows []playoffForecastRow) []playoffImpactMatch {
	if postseason.Unavailable != "" {
		return nil
	}
	seeds := playoffSeedLookup(postseason)
	labels := bracketSlotLabels(format)
	byTeam := func(result simulation.Result) map[string]simulation.TeamResult {
		out := make(map[string]simulation.TeamResult, len(result.Teams))
		for _, team := range result.Teams {
			out[team.Team.ID] = team
		}
		return out
	}
	baseTeams := byTeam(base)

	type pending struct {
		id        string
		round     int
		sides     [2]string
		firstTask int
	}
	last := len(format.Rounds) - 1
	var matches []pending
	var tasks []forecastTask
	for ri, round := range format.Rounds {
		if ri == last {
			// Winning the final is winning the title, so the result adds nothing
			// the table does not already say.
			continue
		}
		for _, slot := range format.Slots {
			if slot.RoundID != round.ID {
				continue
			}
			match, ok := eligible[slot.ID]
			if !ok {
				continue
			}
			if _, fixed := state.PlayoffWinners[slot.ID]; fixed {
				continue
			}
			sides := [2]string{match.Home, match.Away}
			if seeds[sides[0]] > seeds[sides[1]] {
				sides[0], sides[1] = sides[1], sides[0]
			}
			entry := pending{id: slot.ID, round: ri, sides: sides, firstTask: len(tasks)}
			for _, winner := range sides {
				next := state.WithPlayoffWinner(slot.ID, simulation.PostseasonMatch{Home: match.Home, Away: match.Away, Winner: winner})
				scenario := *postseason
				scenario.Scenarios = next.PlayoffWinners
				tasks = append(tasks, a.forecastTasks(data, next, places, &scenario)[0])
			}
			matches = append(matches, entry)
		}
	}
	if len(matches) == 0 {
		return nil
	}
	results, err := a.playoffForecastResults(ctx, tasks, postseason)
	if err != nil {
		return nil
	}

	var views []playoffImpactMatch
	for _, match := range matches {
		view := playoffImpactMatch{ID: match.id, Label: labels[match.id]}
		view.Fixture = playoffTeamName(data, match.sides[0]) + " vs " + playoffTeamName(data, match.sides[1])
		// The winner reaches the next round for certain, so only rounds beyond it
		// still carry a chance worth comparing, then the title itself.
		var reached []competition.BracketRound
		for ri := match.round + 2; ri < len(format.Rounds); ri++ {
			reached = append(reached, format.Rounds[ri])
			view.Metrics = append(view.Metrics, singularRoundLabel(format.Rounds[ri].Label))
		}
		view.Metrics = append(view.Metrics, "Champion")
		texts := func(team simulation.TeamResult) []string {
			if team.ChampionshipEliminated {
				out := make([]string, len(view.Metrics))
				for i := range out {
					out[i] = "Out"
				}
				return out
			}
			out := make([]string, 0, len(view.Metrics))
			for _, round := range reached {
				out = append(out, forecastChance(team.RoundReach[round.ID], team.ReachedRounds[round.ID], false))
			}
			return append(out, forecastChance(team.ChampionshipProbability, team.ChampionshipClinched, false))
		}
		scenarios := [2]map[string]simulation.TeamResult{byTeam(results[match.firstTask]), byTeam(results[match.firstTask+1])}
		for _, id := range match.sides {
			team := baseTeams[id]
			advance := team.RoundReach[format.Rounds[match.round+1].ID]
			view.Outcomes = append(view.Outcomes, playoffImpactOutcome{Team: teamName(team.Team), MobileName: scenarioTeamCode(team.Team), Advance: boundedPercent(advance)})
		}
		for _, row := range rows {
			if row.Out {
				continue
			}
			item := playoffImpactRow{Team: row.Team, MobileName: row.MobileName, SeedText: row.SeedText, Now: texts(baseTeams[row.Team.ID]), Participant: row.Team.ID == match.sides[0] || row.Team.ID == match.sides[1]}
			for _, scenario := range scenarios {
				item.Cells = append(item.Cells, texts(scenario[row.Team.ID]))
			}
			view.Rows = append(view.Rows, item)
		}
		views = append(views, view)
	}
	return views
}
