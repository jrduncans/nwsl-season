package app

import (
	"fmt"
	"sort"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/forecaststate"
	"github.com/jrduncans/nwsl-season/internal/simulation"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

type forecastPage struct {
	Title               string
	Season              string
	Stage               string
	HomePath            string
	StylesheetPath      string
	ScriptPath          string
	CatalogPage         bool
	SeasonPath          string
	Navigation          []navigationItem
	SeasonSelector      []seasonSelectorItem
	StageSelector       []seasonSelectorItem
	SeasonsPath         string
	ForecastPath        string
	ModelEvaluationPath string
	CanonicalPath       string
	ResetPath           string
	ModelName           string
	ModelID             string
	ModelDetail         string
	Models              []forecastModelView
	HasComparison       bool
	ComparisonName      string
	ComparisonID        string
	PlayoffPlaces       int
	XGAvailable         int
	XGCompleted         int
	XGCoverage          string
	XGWarning           bool
	ShowXGCoverage      bool
	XGFreshness         string
	XGFreshnessFallback string
	XGRefreshWarning    bool
	Freshness           string
	FreshnessFallback   string
	DataCutoff          string
	Iterations          int
	FixedCount          int
	Remaining           int
	ScheduleNote        string
	Rows                []forecastRowView
	Assumptions         []forecastAssumptionView
	Teams               []forecastTeamOption
	FilteredTeam        string
	HasTeamFilter       bool
	StateValues         []string
	Fixtures            []forecastFixtureOption
	AllFixtures         []forecastFixtureOption
	CanAdd              bool
	DefaultHomeTeam     string
	DefaultAwayTeam     string
}

type forecastRowView struct {
	Rank                int
	PlayoffLine         bool
	Team                teamNameView
	ExpectedPoints      string
	PointsInterval      string
	TopFourChance       string
	TopFourWidth        string
	PlayoffChance       string
	PlayoffWidth        string
	FinishInterval      string
	ShieldChance        string
	PositionBreakdown   []forecastPositionView
	Comparison          *forecastRowMetrics
	ExpectedPointsDelta string
	TopFourDelta        string
	PlayoffDelta        string
	ShieldDelta         string
	PointsDeltaTone     string
	TopFourDeltaTone    string
	PlayoffDeltaTone    string
	ShieldDeltaTone     string
}
type forecastRowMetrics struct {
	ExpectedPoints, TopFourChance, PlayoffChance, ShieldChance string
	PositionBreakdown                                          []forecastPositionView
}
type forecastModelView struct {
	ID, Name, Detail, Inputs, Assumptions string
	Default, Selected, Comparison         bool
}

type forecastPositionView struct {
	Position    int
	Probability string
}

type forecastAssumptionView struct {
	Fixture    string
	Kickoff    string
	KickoffUTC string
	Outcome    string
	Remove     string
}

type forecastTeamOption struct {
	ID   string
	Name string
}

type forecastFixtureOption struct {
	ID         string
	Kickoff    string
	KickoffUTC string
	HomeTeamID string
	AwayTeamID string
	Home       teamNameView
	Away       teamNameView
}

func forecastRows(result simulation.Result, playoffPlaces int) []forecastRowView {
	rows := make([]forecastRowView, 0, len(result.Teams))
	for index, row := range result.Teams {
		view := forecastRowView{
			Rank:           index + 1,
			PlayoffLine:    index+1 == playoffPlaces,
			Team:           teamName(row.Team),
			ExpectedPoints: fmt.Sprintf("%.1f", row.ExpectedPoints),
			PointsInterval: fmt.Sprintf("%d–%d", row.PointsLow, row.PointsHigh),
			TopFourChance:  percent(row.TopFourProbability),
			TopFourWidth:   percent(row.TopFourProbability),
			PlayoffChance:  percent(row.PlayoffProbability),
			PlayoffWidth:   percent(row.PlayoffProbability),
			FinishInterval: fmt.Sprintf("%d–%d", row.PositionLow, row.PositionHigh),
			ShieldChance:   percent(row.ShieldProbability),
		}
		for index, probability := range row.PositionProbability {
			if probability < .0005 {
				continue
			}
			view.PositionBreakdown = append(view.PositionBreakdown, forecastPositionView{Position: index + 1, Probability: percent(probability)})
		}
		rows = append(rows, view)
	}
	return rows
}

func forecastComparisonRows(active simulation.Result, comparison *simulation.Result, playoffPlaces int) []forecastRowView {
	rows := forecastRows(active, playoffPlaces)
	if comparison == nil {
		return rows
	}
	byID := map[string]simulation.TeamResult{}
	for _, row := range comparison.Teams {
		byID[row.Team.ID] = row
	}
	for i := range rows {
		other := byID[active.Teams[i].Team.ID]
		metrics := forecastRowMetrics{ExpectedPoints: fmt.Sprintf("%.1f", other.ExpectedPoints), TopFourChance: percent(other.TopFourProbability), PlayoffChance: percent(other.PlayoffProbability), ShieldChance: percent(other.ShieldProbability)}
		for p, prob := range other.PositionProbability {
			if prob > 0 {
				metrics.PositionBreakdown = append(metrics.PositionBreakdown, forecastPositionView{Position: p + 1, Probability: percent(prob)})
			}
		}
		rows[i].Comparison = &metrics
		source := active.Teams[i]
		// The selected model is the subject of the comparison: every label
		// describes what it projects relative to the optional comparison model.
		pointsDelta := source.ExpectedPoints - other.ExpectedPoints
		topFourDelta := (source.TopFourProbability - other.TopFourProbability) * 100
		playoffDelta := (source.PlayoffProbability - other.PlayoffProbability) * 100
		shieldDelta := (source.ShieldProbability - other.ShieldProbability) * 100
		rows[i].ExpectedPointsDelta = comparisonChangeLabel(pointsDelta, "more points", "fewer points")
		rows[i].TopFourDelta = comparisonChangeLabel(topFourDelta, "pp higher", "pp lower")
		rows[i].PlayoffDelta = comparisonChangeLabel(playoffDelta, "pp higher", "pp lower")
		rows[i].ShieldDelta = comparisonChangeLabel(shieldDelta, "pp higher", "pp lower")
		rows[i].PointsDeltaTone = comparisonTone(pointsDelta, true)
		rows[i].TopFourDeltaTone = comparisonTone(topFourDelta, true)
		rows[i].PlayoffDeltaTone = comparisonTone(playoffDelta, true)
		rows[i].ShieldDeltaTone = comparisonTone(shieldDelta, true)
	}
	return rows
}
func comparisonChangeLabel(value float64, positive, negative string) string {
	if value > -.05 && value < .05 {
		return "No material change"
	}
	if value < 0 {
		return fmt.Sprintf("%.1f %s", -value, negative)
	}
	return fmt.Sprintf("%.1f %s", value, positive)
}

func comparisonTone(value float64, higherIsBetter bool) string {
	if value > .05 {
		if higherIsBetter {
			return "better"
		}
		return "worse"
	}
	if value < -.05 {
		if higherIsBetter {
			return "worse"
		}
		return "better"
	}
	return "neutral"
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

func forecastFixtures(data cache.SeasonData, state forecaststate.State, location *time.Location, teamID string) []forecastFixtureOption {
	teams := make(map[string]teamNameView, len(data.Teams))
	for _, team := range data.Teams {
		teams[team.ID] = teamName(team)
	}
	options := make([]forecastFixtureOption, 0)
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
		kickoff, err := fixtures.ParseKickoff(game.KickoffUTC)
		if err != nil {
			continue
		}
		home, away := teams[game.HomeTeamID], teams[game.AwayTeamID]
		options = append(options, forecastFixtureOption{
			ID: game.ASAID, Kickoff: kickoff.In(location).Format("Mon Jan 2, 3:04 PM MST"), KickoffUTC: kickoff.UTC().Format(time.RFC3339),
			HomeTeamID: game.HomeTeamID, AwayTeamID: game.AwayTeamID, Home: home, Away: away,
		})
	}
	return options
}

func forecastAssumptions(data cache.SeasonData, state forecaststate.State, removeURL func(string) string, location *time.Location) []forecastAssumptionView {
	byID := make(map[string]cache.Game, len(data.Games))
	for _, game := range data.Games {
		byID[game.ASAID] = game
	}
	values := make([]forecastAssumptionView, 0, len(state.Fixed))
	for gameID, outcome := range state.Fixed {
		game := byID[gameID]
		kickoff, _ := fixtures.ParseKickoff(game.KickoffUTC)
		fixture := fmt.Sprintf("%s vs %s", teamDisplay(data.Teams, game.HomeTeamID), teamDisplay(data.Teams, game.AwayTeamID))
		values = append(values, forecastAssumptionView{
			Fixture: fixture, Kickoff: kickoff.In(location).Format("Mon Jan 2, 3:04 PM MST"), KickoffUTC: kickoff.UTC().Format(time.RFC3339),
			Outcome: outcomeLabel(data.Teams, game, outcome), Remove: removeURL(gameID),
		})
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
