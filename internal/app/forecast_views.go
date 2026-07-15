package app

import (
	"fmt"
	"sort"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/forecaststate"
	"github.com/jrduncans/nwsl-season/internal/simulation"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

type forecastPage struct {
	Title          string
	Season         string
	HomePath       string
	StylesheetPath string
	ScriptPath     string
	SeasonPath     string
	ForecastPath   string
	CanonicalPath  string
	ResetPath      string
	ModelName      string
	ModelID        string
	ModelDetail    string
	DataCutoff     string
	Iterations     int
	FixedCount     int
	Remaining      int
	ScheduleNote   string
	Rows           []forecastRowView
	Assumptions    []forecastAssumptionView
	Teams          []forecastTeamOption
	FilteredTeam   string
	HasTeamFilter  bool
	StateValues    []string
	Fixtures       []forecastFixtureOption
	CanAdd         bool
}

type forecastRowView struct {
	Team              teamNameView
	ExpectedPoints    string
	PointsInterval    string
	PlayoffChance     string
	ExpectedFinish    string
	FinishInterval    string
	ShieldChance      string
	PositionBreakdown []forecastPositionView
}

type forecastPositionView struct {
	Position    int
	Probability string
}

type forecastAssumptionView struct {
	Fixture string
	Outcome string
	Remove  string
}

type forecastTeamOption struct {
	ID   string
	Name string
}

type forecastFixtureOption struct {
	ID      string
	Kickoff string
	Home    teamNameView
	Away    teamNameView
}

func forecastRows(result simulation.Result) []forecastRowView {
	rows := make([]forecastRowView, 0, len(result.Teams))
	for _, row := range result.Teams {
		view := forecastRowView{
			Team:           teamName(row.Team),
			ExpectedPoints: fmt.Sprintf("%.1f", row.ExpectedPoints),
			PointsInterval: fmt.Sprintf("%d–%d", row.PointsLow, row.PointsHigh),
			PlayoffChance:  percent(row.PlayoffProbability),
			ExpectedFinish: fmt.Sprintf("%.1f", row.ExpectedPosition),
			FinishInterval: fmt.Sprintf("%d–%d", row.PositionLow, row.PositionHigh),
			ShieldChance:   percent(row.ShieldProbability),
		}
		for index, probability := range row.PositionProbability {
			if probability == 0 {
				continue
			}
			view.PositionBreakdown = append(view.PositionBreakdown, forecastPositionView{Position: index + 1, Probability: percent(probability)})
		}
		rows = append(rows, view)
	}
	return rows
}

func percent(value float64) string { return fmt.Sprintf("%.1f%%", value*100) }

func forecastTeamOptions(teams []standings.Team) []forecastTeamOption {
	options := make([]forecastTeamOption, 0, len(teams))
	for _, team := range teams {
		options = append(options, forecastTeamOption{ID: team.ID, Name: displayName(team)})
	}
	sort.Slice(options, func(i, j int) bool {
		if options[i].Name != options[j].Name {
			return options[i].Name < options[j].Name
		}
		return options[i].ID < options[j].ID
	})
	return options
}

func forecastFixtures(data cache.SeasonData, state forecaststate.State, teamID string, location *time.Location) []forecastFixtureOption {
	teams := make(map[string]teamNameView, len(data.Teams))
	for _, team := range data.Teams {
		teams[team.ID] = teamName(team)
	}
	fixtures := make([]forecastFixtureOption, 0)
	for _, game := range data.Games {
		if game.Status != simulation.RemainingStatus {
			continue
		}
		if _, fixed := state.Fixed[game.ASAID]; fixed {
			continue
		}
		if teamID != "" && game.HomeTeamID != teamID && game.AwayTeamID != teamID {
			continue
		}
		kickoff, err := parseKickoff(game.KickoffUTC)
		if err != nil {
			continue
		}
		fixtures = append(fixtures, forecastFixtureOption{
			ID: game.ASAID, Kickoff: kickoff.In(location).Format("Mon Jan 2, 3:04 PM MST"),
			Home: teams[game.HomeTeamID], Away: teams[game.AwayTeamID],
		})
	}
	return fixtures
}

func forecastAssumptions(data cache.SeasonData, state forecaststate.State, removeURL func(string) string, location *time.Location) []forecastAssumptionView {
	byID := make(map[string]cache.Game, len(data.Games))
	for _, game := range data.Games {
		byID[game.ASAID] = game
	}
	values := make([]forecastAssumptionView, 0, len(state.Fixed))
	for gameID, outcome := range state.Fixed {
		game := byID[gameID]
		kickoff, _ := parseKickoff(game.KickoffUTC)
		fixture := fmt.Sprintf("%s · %s vs %s", kickoff.In(location).Format("Mon Jan 2"), teamDisplay(data.Teams, game.HomeTeamID), teamDisplay(data.Teams, game.AwayTeamID))
		values = append(values, forecastAssumptionView{fixture, outcomeLabel(data.Teams, game, outcome), removeURL(gameID)})
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Fixture < values[j].Fixture })
	return values
}

func teamDisplay(teams []standings.Team, id string) string {
	for _, team := range teams {
		if team.ID == id {
			return displayName(team)
		}
	}
	return id
}

func outcomeLabel(teams []standings.Team, game cache.Game, outcome simulation.Outcome) string {
	switch outcome {
	case simulation.HomeWin:
		return teamDisplay(teams, game.HomeTeamID) + " win"
	case simulation.AwayWin:
		return teamDisplay(teams, game.AwayTeamID) + " win"
	default:
		return "Draw"
	}
}
