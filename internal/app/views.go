package app

import (
	"fmt"
	"math"
	"net/url"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/scenarios"
	"github.com/jrduncans/nwsl-season/internal/standings"
	"github.com/jrduncans/nwsl-season/internal/strength"
)

const clubLogoBaseURL = "https://american-soccer-analysis-headshots.s3.amazonaws.com/club_logos/"

type seasonPage struct {
	Title                  string
	Season                 string
	Stage                  string
	HomePath               string
	StylesheetPath         string
	ScriptPath             string
	SeasonPath             string
	FixturesPath           string
	ScheduleDifficultyPath string
	ForecastPath           string
	XGPath                 string
	CurrentPath            string
	OutlookPath            string
	Outlook                bool
	OutlookModel           string
	OutlookRows            []forecastRowView
	Freshness              string
	FreshnessFallback      string
	ScheduleNote           string
	Standings              []tableRowView
	Strength               strengthView
	FixtureGroups          []fixtureGroupView
	Remaining              int
}

type strengthView struct {
	Rows                []strengthRowView
	CompletedMatches    int
	RemainingMatches    int
	AvailableRows       int
	Baseline            string
	RawBaseline         string
	HasBaseline         bool
	HasRawBaseline      bool
	BaselinePosition    string
	RawBaselinePosition string
	HomePPG             string
	AwayPPG             string
	VenueGap            string
	HasCallouts         bool
	Toughest            strengthRowView
	Easiest             strengthRowView
}

type strengthRowView struct {
	TeamID                   string
	Position                 int
	Team                     teamNameView
	RemainingFixtures        int
	RemainingHome            int
	RemainingAway            int
	HomeOpponentPPG          string
	AwayOpponentPPG          string
	RawOpponentPPG           string
	VenueAdjustedOpponentPPG string
	DeltaFromBaseline        string
	ScheduleLabel            string
	SchedulePosition         string
	ScheduleDirection        string
	ScheduleScale            string
	ScheduleOpacity          string
	PlotPosition             string
	RawPlotPosition          string
	Fixtures                 []strengthFixtureView
	Available                bool
}

type strengthFixtureView struct {
	ID                       string
	Opponent                 teamNameView
	Venue                    string
	OpponentPPG              string
	VenueAdjustedOpponentPPG string
	Available                bool
}

type tableRowView struct {
	Position                  int
	TeamID                    string
	Team                      teamNameView
	Played                    int
	Wins                      int
	Draws                     int
	Losses                    int
	GoalsFor                  int
	GoalsAgainst              int
	GoalDifference            string
	Points                    int
	PointsPerGame             string
	GoalsForPerGame           string
	GoalsAgainstPerGame       string
	GoalDifferencePerGame     string
	PlayoffLine               bool
	TotalPosition             int
	TotalPlayoffLine          bool
	QualificationBadge        string
	QualificationTitle        string
	QualificationAchievements string
	TieBreak                  string
	ScheduleAvailable         bool
	ScheduleLabel             string
	ScheduleDelta             string
	SchedulePosition          string
	ScheduleDirection         string
	ScheduleScale             string
	ScheduleOpacity           string
	ScheduleRemaining         int
	ScheduleHome              int
	ScheduleAway              int
}

func addTotalPositions(rows []tableRowView, totalTable []standings.TableRow, playoffPlaces int) []tableRowView {
	positions := make(map[string]int, len(totalTable))
	for index, row := range totalTable {
		positions[row.Team.ID] = index + 1
	}
	for index := range rows {
		rows[index].TotalPosition = positions[rows[index].TeamID]
		rows[index].TotalPlayoffLine = rows[index].TotalPosition == playoffPlaces
	}
	return rows
}

type fixtureGroupView struct {
	Label string
	Games []fixtureView
}

type fixtureView struct {
	ID        string
	Kickoff   string
	HomeTeam  teamNameView
	AwayTeam  teamNameView
	Score     string
	Completed bool
	Remaining bool
	Status    string
}

type teamNameView struct {
	Name    string
	LogoURL string
}

type errorPage struct {
	Title          string
	Message        string
	HomePath       string
	StylesheetPath string
	ScriptPath     string
}
type clinchingPage struct {
	seasonPage
	ClinchingPath string
	State         string
	Slate         scenarios.Slate
	Rows          []clinchingRowView
}
type clinchingRowView struct {
	Team, Achievement, State, Limitation string
	Already                              bool
	Clauses, Necessary                   []string
}

func tableViews(table []standings.TableRow, playoffPlaces int, clinched map[string]bool) []tableRowView {
	rows := make([]tableRowView, 0, len(table))
	for index, row := range table {
		gd := row.Record.GoalDifference()
		gdText := intText(gd)
		if gd > 0 {
			gdText = "+" + gdText
		}
		tieBreak := ""
		if row.TieBreak.Undetermined {
			tieBreak = "Official order unresolved: " + row.TieBreak.Reason
		}
		rows = append(rows, tableRowView{
			Position: index + 1, TeamID: row.Team.ID, Team: teamName(row.Team),
			Played: row.Record.Played, Wins: row.Record.Wins, Draws: row.Record.Draws, Losses: row.Record.Losses,
			GoalsFor: row.Record.GoalsFor, GoalsAgainst: row.Record.GoalsAgainst, GoalDifference: gdText,
			Points: row.Record.Points, PointsPerGame: fmt.Sprintf("%.2f", pointsPerGame(row.Record)),
			GoalsForPerGame:       perGameText(row.Record.GoalsFor, row.Record.Played),
			GoalsAgainstPerGame:   perGameText(row.Record.GoalsAgainst, row.Record.Played),
			GoalDifferencePerGame: signedPerGameText(gd, row.Record.Played),
			PlayoffLine:           index+1 == playoffPlaces, TieBreak: tieBreak,
		})
	}
	return rows
}

func strengthViewFrom(result strength.Result) strengthView {
	rows := make([]strengthRowView, 0, len(result.Rows))
	for index, row := range result.Rows {
		view := strengthRowView{
			TeamID: row.Team.ID, Position: index + 1, Team: teamName(row.Team), RemainingFixtures: row.RemainingFixtures,
			RemainingHome: row.RemainingHome, RemainingAway: row.RemainingAway, Available: row.Available,
			ScheduleLabel: row.ScheduleLabel,
		}
		if row.Available {
			view.HomeOpponentPPG = fmt.Sprintf("%.2f", row.HomeOpponentPPG)
			view.AwayOpponentPPG = fmt.Sprintf("%.2f", row.AwayOpponentPPG)
			view.RawOpponentPPG = fmt.Sprintf("%.2f", row.RawOpponentPPG)
			view.VenueAdjustedOpponentPPG = fmt.Sprintf("%.2f", row.VenueAdjustedOpponentPPG)
			view.DeltaFromBaseline = signedFloatText(row.DeltaFromBaseline)
		} else {
			view.HomeOpponentPPG, view.AwayOpponentPPG = "—", "—"
			view.RawOpponentPPG, view.VenueAdjustedOpponentPPG = "—", "—"
			view.DeltaFromBaseline, view.ScheduleLabel = "—", "Unavailable"
		}
		for _, fixture := range row.Fixtures {
			fixtureView := strengthFixtureView{
				ID: fixture.ID, Opponent: teamName(fixture.Opponent),
				Venue: "Away", Available: fixture.Available,
			}
			if fixture.Home {
				fixtureView.Venue = "Home"
			}
			if fixture.Available {
				fixtureView.OpponentPPG = fmt.Sprintf("%.2f", fixture.OpponentPPG)
				fixtureView.VenueAdjustedOpponentPPG = fmt.Sprintf("%.2f", fixture.VenueAdjustedOpponentPPG)
			} else {
				fixtureView.OpponentPPG = "—"
				fixtureView.VenueAdjustedOpponentPPG = "—"
			}
			view.Fixtures = append(view.Fixtures, fixtureView)
		}
		rows = append(rows, view)
	}
	var adjustedMin, adjustedMax, rawMin, rawMax float64
	var adjustedSum, rawSum float64
	availableCount := 0
	for _, row := range result.Rows {
		if !row.Available {
			continue
		}
		if availableCount == 0 || row.VenueAdjustedOpponentPPG < adjustedMin {
			adjustedMin = row.VenueAdjustedOpponentPPG
		}
		if availableCount == 0 || row.VenueAdjustedOpponentPPG > adjustedMax {
			adjustedMax = row.VenueAdjustedOpponentPPG
		}
		if availableCount == 0 || row.RawOpponentPPG < rawMin {
			rawMin = row.RawOpponentPPG
		}
		if availableCount == 0 || row.RawOpponentPPG > rawMax {
			rawMax = row.RawOpponentPPG
		}
		adjustedSum += row.VenueAdjustedOpponentPPG
		rawSum += row.RawOpponentPPG
		availableCount++
	}
	for index := range rows {
		if !rows[index].Available {
			continue
		}
		rows[index].PlotPosition = plotPosition(result.Rows[index].VenueAdjustedOpponentPPG, adjustedMin, adjustedMax)
		rows[index].RawPlotPosition = plotPosition(result.Rows[index].RawOpponentPPG, rawMin, rawMax)
	}
	maxDelta := 0.0
	for _, row := range result.Rows {
		if row.Available && math.Abs(row.DeltaFromBaseline) > maxDelta {
			maxDelta = math.Abs(row.DeltaFromBaseline)
		}
	}
	for index := range rows {
		if !rows[index].Available {
			continue
		}
		delta := result.Rows[index].DeltaFromBaseline
		ratio := 0.0
		if maxDelta > 0 {
			ratio = math.Abs(delta) / maxDelta
		}
		position := 50.0
		if maxDelta > 0 {
			// Keep the scaled marker inside the compact track with a small
			// visual margin at either edge.
			position += delta / maxDelta * 38
		}
		direction := "average"
		if delta > 0 {
			direction = "harder"
		} else if delta < 0 {
			direction = "easier"
		}
		rows[index].SchedulePosition = fmt.Sprintf("%.1f", position)
		rows[index].ScheduleDirection = direction
		rows[index].ScheduleScale = fmt.Sprintf("%.2f", 0.85+ratio*0.55)
		rows[index].ScheduleOpacity = fmt.Sprintf("%.2f", 0.45+ratio*0.55)
	}
	view := strengthView{
		Rows: rows, CompletedMatches: result.CompletedMatches, RemainingMatches: result.RemainingMatches,
		AvailableRows: result.AvailableRows, HasBaseline: result.AvailableRows > 0,
		Baseline:         fmt.Sprintf("%.2f", result.Baseline),
		BaselinePosition: plotPosition(result.Baseline, adjustedMin, adjustedMax),
		HomePPG:          fmt.Sprintf("%.2f", result.HomePPG), AwayPPG: fmt.Sprintf("%.2f", result.AwayPPG), VenueGap: signedFloatText(result.VenueGap),
	}
	if availableCount > 0 {
		view.HasRawBaseline = true
		view.RawBaseline = fmt.Sprintf("%.2f", rawSum/float64(availableCount))
		view.RawBaselinePosition = plotPosition(rawSum/float64(availableCount), rawMin, rawMax)
	}
	for index := range rows {
		if !rows[index].Available {
			continue
		}
		if !view.HasCallouts {
			view.Toughest, view.Easiest, view.HasCallouts = rows[index], rows[index], true
			continue
		}
		view.Easiest = rows[index]
	}
	return view
}

func plotPosition(value, minimum, maximum float64) string {
	if maximum <= minimum {
		return "50.0"
	}
	position := (value - minimum) / (maximum - minimum) * 100
	if position < 5 {
		position = 5
	}
	if position > 95 {
		position = 95
	}
	return fmt.Sprintf("%.1f", position)
}

func addScheduleIndicators(rows []tableRowView, strength strengthView) []tableRowView {
	byID := make(map[string]strengthRowView, len(strength.Rows))
	for _, row := range strength.Rows {
		byID[row.TeamID] = row
	}
	for index := range rows {
		if row, ok := byID[rows[index].TeamID]; ok {
			rows[index].ScheduleAvailable = row.Available
			rows[index].ScheduleLabel = row.ScheduleLabel
			rows[index].ScheduleDelta = row.DeltaFromBaseline
			rows[index].SchedulePosition = row.SchedulePosition
			rows[index].ScheduleDirection = row.ScheduleDirection
			rows[index].ScheduleScale = row.ScheduleScale
			rows[index].ScheduleOpacity = row.ScheduleOpacity
			rows[index].ScheduleRemaining = row.RemainingFixtures
			rows[index].ScheduleHome = row.RemainingHome
			rows[index].ScheduleAway = row.RemainingAway
		}
	}
	return rows
}

func fixtureGroups(data cache.SeasonData, location *time.Location) []fixtureGroupView {
	teams := make(map[string]teamNameView, len(data.Teams))
	for _, team := range data.Teams {
		teams[team.ID] = teamName(team)
	}
	groups := []fixtureGroupView{}
	groupIndex := map[string]int{}
	for _, game := range data.Games {
		kickoff, _ := parseKickoff(game.KickoffUTC)
		localKickoff := kickoff.In(location)
		label := localKickoff.Format("Monday, January 2")
		if game.Matchday.Valid {
			label = fmt.Sprintf("Matchday %d", game.Matchday.Int64)
		}
		index, ok := groupIndex[label]
		if !ok {
			index = len(groups)
			groupIndex[label] = index
			groups = append(groups, fixtureGroupView{Label: label})
		}
		view := fixtureView{
			ID: game.ASAID, Kickoff: localKickoff.Format("Mon Jan 2, 3:04 PM MST"),
			HomeTeam: teams[game.HomeTeamID], AwayTeam: teams[game.AwayTeamID],
			Completed: game.Status == standings.CompletedStatus,
			Remaining: game.Status == remainingStatus,
			Status:    game.Status,
		}
		if view.Completed && game.HomeScore.Valid && game.AwayScore.Valid {
			view.Score = fmt.Sprintf("%d–%d", game.HomeScore.Int64, game.AwayScore.Int64)
		}
		groups[index].Games = append(groups[index].Games, view)
	}
	return groups
}

func displayName(team standings.Team) string {
	for _, value := range []string{team.Name, team.ShortName, team.Abbreviation, team.ID} {
		if value != "" {
			return value
		}
	}
	return "Unknown team"
}

func teamName(team standings.Team) teamNameView {
	return teamNameView{Name: displayName(team), LogoURL: clubLogoURL(team.ID)}
}

func clubLogoURL(teamID string) string {
	if teamID == "" {
		return ""
	}
	return clubLogoBaseURL + url.PathEscape(teamID) + ".png"
}

func pointsPerGame(record standings.Record) float64 {
	if record.Played == 0 {
		return 0
	}
	return float64(record.Points) / float64(record.Played)
}

func perGameText(value, played int) string {
	if played == 0 {
		return "0.00"
	}
	return fmt.Sprintf("%.2f", float64(value)/float64(played))
}

func signedPerGameText(value, played int) string {
	text := perGameText(value, played)
	if value > 0 {
		return "+" + text
	}
	return text
}

func signedFloatText(value float64) string {
	text := fmt.Sprintf("%.2f", value)
	if value > 0 {
		return "+" + text
	}
	return text
}
