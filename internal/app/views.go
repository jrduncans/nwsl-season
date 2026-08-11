package app

import (
	"fmt"
	"math"
	"net/url"
	"sort"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
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
	ClinchingPath          string
	CurrentPath            string
	Navigation             []navigationItem
	SeasonSelector         []seasonSelectorItem
	Freshness              string
	FreshnessFallback      string
	ScheduleNote           string
	XGWarning              string
	FormatNotice           string
	HasStandings           bool
	HasFixtures            bool
	HasXG                  bool
	HasScheduleDifficulty  bool
	HasForecast            bool
	Standings              []tableRowView
	Strength               strengthView
	ResultFixtureGroups    []fixtureGroupView
	UpcomingFixtureGroups  []fixtureGroupView
	FixtureTeams           []teamNameView
	Remaining              int
	HasFixtureOutlooks     bool
}

type navigationItem struct {
	Label   string
	Path    string
	Current bool
}

type seasonSelectorItem struct {
	Label    string
	Path     string
	Selected bool
}

func seasonSelector(from, selectedSeason string) []seasonSelectorItem {
	entries := competition.PublicEntries()
	items := make([]seasonSelectorItem, 0, len(entries))
	for _, entry := range entries {
		items = append(items, seasonSelectorItem{
			Label: entry.Label, Path: relativeURL(from, "/seasons/"+url.PathEscape(entry.Season)), Selected: entry.Season == selectedSeason,
		})
	}
	return items
}

func seasonNavigation(from string, scope requestCompetition, current string, rules competition.Rules, verified bool) []navigationItem {
	base := "/seasons/" + url.PathEscape(scope.Season)
	items := []struct {
		label string
		path  string
	}{}
	if scope.standingsAvailable() {
		items = append(items, struct{ label, path string }{"Standings", base})
	}
	if scope.fixturesAvailable() {
		items = append(items, struct{ label, path string }{"Results & fixtures", base + "/fixtures"})
	}
	if scope.scheduleDifficultyAvailable() {
		items = append(items, struct{ label, path string }{"Schedule difficulty", base + "/schedule-difficulty"})
	}
	if scope.clinchingAvailable(rules, verified) {
		items = append(items, struct{ label, path string }{"Clinching scenarios", base + "/clinching"})
	}
	if scope.forecastAvailable(rules, verified) {
		items = append(items, struct{ label, path string }{"Forecast lab", base + "/forecast"})
	}
	navigation := make([]navigationItem, 0, len(items))
	for _, item := range items {
		navigation = append(navigation, navigationItem{
			Label: item.label, Path: relativeURL(from, item.path), Current: current == item.path,
		})
	}
	return navigation
}

type strengthView struct {
	Rows                   []strengthRowView
	CompletedMatches       int
	RemainingMatches       int
	AvailableRows          int
	ComparableRows         int
	Baseline               string
	RawBaseline            string
	HasBaseline            bool
	HasRawBaseline         bool
	HasIndividualEstimates bool
	BaselinePosition       string
	RawBaselinePosition    string
	HomePPG                string
	AwayPPG                string
	VenueGap               string
	HasCallouts            bool
	Toughest               strengthRowView
	Easiest                strengthRowView
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
	NoRemainingFixtures      bool
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
	Position              int
	TeamID                string
	Team                  teamNameView
	Played                int
	Wins                  int
	Draws                 int
	Losses                int
	GoalsFor              int
	GoalsAgainst          int
	GoalDifference        string
	Points                int
	PointsPerGame         string
	GoalsForPerGame       string
	GoalsAgainstPerGame   string
	GoalDifferencePerGame string
	XGForAgainst          string
	XGForAgainstPerGame   string
	XGDifference          string
	XGDifferencePerGame   string
	XPoints               string
	XPointsPerGame        string
	PlayoffLine           bool
	TotalPosition         int
	TotalPlayoffLine      bool
	QualificationBadge    string
	QualificationTitle    string
	TieBreak              string
	ScheduleAvailable     bool
	ScheduleLabel         string
	ScheduleDelta         string
	SchedulePosition      string
	ScheduleDirection     string
	ScheduleScale         string
	ScheduleOpacity       string
	ScheduleRemaining     int
	ScheduleHome          int
	ScheduleAway          int
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
	Label      string
	StartUTC   string
	InProgress bool
	Games      []fixtureView
}

type fixtureView struct {
	ID         string
	Kickoff    string
	KickoffUTC string
	HomeTeam   teamNameView
	AwayTeam   teamNameView
	Score      string
	XG         string
	Completed  bool
	Remaining  bool
	Status     string
	Outlook    *fixtureOutlookView
}

// fixtureOutlookView is the pre-match, three-way result distribution shown on
// the fixtures page. Its shares remain numeric so the same values drive the
// displayed percentages and the proportional bar.
type fixtureOutlookView struct {
	HomeWin, Draw, AwayWin             float64
	HomeWinText, DrawText, AwayWinText string
}

type teamNameView struct {
	ID      string
	Name    string
	LogoURL string
}

type errorPage struct {
	Title             string
	Message           string
	HomePath          string
	StylesheetPath    string
	ScriptPath        string
	Navigation        []navigationItem
	SeasonSelector    []seasonSelectorItem
	Freshness         string
	FreshnessFallback string
}
type clinchingPage struct {
	seasonPage
	State            string
	Slate            scenarios.Slate
	SlateStartsAtUTC string
	SlateStartsAt    string
	SlateLatestUTC   string
	SlateLatest      string
	SlateCutoffUTC   string
	SlateCutoff      string
	SlateGroups      []fixtureGroupView
	Actionable       []clinchingRowView
	NoHelp           []clinchingRowView
	NoHelpTeams      []clinchingTeamView
	Elimination      []clinchingRowView
	AlreadyClinched  []clinchingRowView
	ClinchingTeams   []teamNameView
}
type clinchingRowView struct {
	Team                               teamNameView
	Achievement, Limitation            string
	Clauses, Necessary                 []string
	NoHelp                             string
	NoHelpFixtures                     string
	NoHelpFixtureCount                 int
	AchievementRank, StandingsPosition int
	AlreadyEliminated                  bool
}

type clinchingTeamView struct {
	Team  teamNameView
	Paths []clinchingRowView
}

func tableViews(table []standings.TableRow, playoffPlaces int) []tableRowView {
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

// addXGValues adds team-model xG only for completed fixtures. A partial xG
// refresh must never make an incomplete total look like a complete one.
func addXGValues(rows []tableRowView, data cache.SeasonData) ([]tableRowView, int, int) {
	type total struct {
		matches        int
		goalsFor       float64
		goalsAgainst   float64
		xPoints        float64
		xPointsMatches int
	}

	completed := make(map[string]cache.Game)
	for _, game := range data.Games {
		if game.Status == fixtures.CompletedStatus {
			completed[game.ASAID] = game
		}
	}
	totals := make(map[string]*total, len(data.Teams))
	for _, team := range data.Teams {
		totals[team.ID] = &total{}
	}
	available := 0
	seen := make(map[string]bool)
	for _, xg := range data.XGoals {
		game, completedGame := completed[xg.GameID]
		if !completedGame || seen[xg.GameID] || xg.Availability != cache.XGAvailable || !xg.HomeXG.Valid || !xg.AwayXG.Valid {
			continue
		}
		home, away := totals[game.HomeTeamID], totals[game.AwayTeamID]
		if home == nil || away == nil {
			continue
		}
		seen[xg.GameID] = true
		available++
		home.matches++
		home.goalsFor += xg.HomeXG.Float64
		home.goalsAgainst += xg.AwayXG.Float64
		away.matches++
		away.goalsFor += xg.AwayXG.Float64
		away.goalsAgainst += xg.HomeXG.Float64
		if xg.HomeXPoints.Valid && xg.AwayXPoints.Valid {
			home.xPoints += xg.HomeXPoints.Float64
			home.xPointsMatches++
			away.xPoints += xg.AwayXPoints.Float64
			away.xPointsMatches++
		}
	}
	for index := range rows {
		value := totals[rows[index].TeamID]
		if value == nil || value.matches == 0 {
			rows[index].XGForAgainst, rows[index].XGForAgainstPerGame = "—", "—"
			rows[index].XGDifference, rows[index].XGDifferencePerGame = "—", "—"
			rows[index].XPoints, rows[index].XPointsPerGame = "—", "—"
			continue
		}
		difference := value.goalsFor - value.goalsAgainst
		rows[index].XGForAgainst = fmt.Sprintf("%.2f/%.2f", value.goalsFor, value.goalsAgainst)
		rows[index].XGForAgainstPerGame = fmt.Sprintf("%.2f/%.2f", value.goalsFor/float64(value.matches), value.goalsAgainst/float64(value.matches))
		rows[index].XGDifference = signedFloatText(difference)
		rows[index].XGDifferencePerGame = signedFloatText(difference / float64(value.matches))
		if value.xPointsMatches == value.matches {
			rows[index].XPoints = fmt.Sprintf("%.2f", value.xPoints)
			rows[index].XPointsPerGame = fmt.Sprintf("%.2f", value.xPoints/float64(value.matches))
		} else {
			rows[index].XPoints, rows[index].XPointsPerGame = "—", "—"
		}
	}
	return rows, available, len(completed)
}

func strengthViewFrom(result strength.Result) strengthView {
	hasBaseline := result.ComparableRows > 1 && result.AvailableRows == result.ComparableRows
	rows := make([]strengthRowView, 0, len(result.Rows))
	for index, row := range result.Rows {
		view := strengthRowView{
			TeamID: row.Team.ID, Position: index + 1, Team: teamName(row.Team), RemainingFixtures: row.RemainingFixtures,
			RemainingHome: row.RemainingHome, RemainingAway: row.RemainingAway, Available: row.Available,
			ScheduleLabel: row.ScheduleLabel, NoRemainingFixtures: row.UnavailableReason == strength.UnavailableNoRemainingFixtures,
		}
		if row.Available {
			if row.RemainingHome > 0 {
				view.HomeOpponentPPG = fmt.Sprintf("%.2f", row.HomeOpponentPPG)
			} else {
				view.HomeOpponentPPG = "—"
			}
			if row.RemainingAway > 0 {
				view.AwayOpponentPPG = fmt.Sprintf("%.2f", row.AwayOpponentPPG)
			} else {
				view.AwayOpponentPPG = "—"
			}
			view.RawOpponentPPG = fmt.Sprintf("%.2f", row.RawOpponentPPG)
			view.VenueAdjustedOpponentPPG = fmt.Sprintf("%.2f", row.VenueAdjustedOpponentPPG)
			if hasBaseline {
				view.DeltaFromBaseline = scheduleDeltaText(row.DeltaFromBaseline)
			} else {
				view.DeltaFromBaseline, view.ScheduleLabel = "—", "Not comparable"
			}
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
		AvailableRows: result.AvailableRows, ComparableRows: result.ComparableRows,
		HasBaseline: hasBaseline, HasIndividualEstimates: result.AvailableRows > 0,
		Baseline:         fmt.Sprintf("%.2f", result.Baseline),
		BaselinePosition: plotPosition(result.Baseline, adjustedMin, adjustedMax),
		HomePPG:          fmt.Sprintf("%.2f", result.HomePPG), AwayPPG: fmt.Sprintf("%.2f", result.AwayPPG), VenueGap: signedFloatText(result.VenueGap),
	}
	if hasBaseline {
		view.HasRawBaseline = true
		view.RawBaseline = fmt.Sprintf("%.2f", rawSum/float64(availableCount))
		view.RawBaselinePosition = plotPosition(rawSum/float64(availableCount), rawMin, rawMax)
	}
	for index := range rows {
		if !hasBaseline {
			break
		}
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
			rows[index].ScheduleAvailable = strength.HasBaseline && row.Available
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
	return fixtureGroupsWithOutlooks(data, location, nil)
}

func fixtureGroupsWithOutlooks(data cache.SeasonData, location *time.Location, outlooks map[string]fixtureOutlookView) []fixtureGroupView {
	return fixtureGroupsWithOutlooksFor(data, location, outlooks, true)
}

func fixtureGroupsWithOutlooksFor(data cache.SeasonData, location *time.Location, outlooks map[string]fixtureOutlookView, includeXG bool) []fixtureGroupView {
	teams := make(map[string]teamNameView, len(data.Teams))
	for _, team := range data.Teams {
		teams[team.ID] = teamName(team)
	}
	xgoals := make(map[string]cache.GameXG, len(data.XGoals))
	for _, xg := range data.XGoals {
		if xg.Availability == cache.XGAvailable && xg.HomeXG.Valid && xg.AwayXG.Valid {
			xgoals[xg.GameID] = xg
		}
	}
	groups := []fixtureGroupView{}
	groupIndex := map[string]int{}
	for _, game := range data.Games {
		kickoff, _ := fixtures.ParseKickoff(game.KickoffUTC)
		localKickoff := kickoff.In(location)
		label := localKickoff.Format("Monday, January 2")
		if game.Matchday.Valid {
			label = fmt.Sprintf("Matchday %d", game.Matchday.Int64)
		}
		index, ok := groupIndex[label]
		if !ok {
			index = len(groups)
			groupIndex[label] = index
			groups = append(groups, fixtureGroupView{Label: label, StartUTC: kickoff.UTC().Format(time.RFC3339)})
		} else if existing, err := time.Parse(time.RFC3339, groups[index].StartUTC); err != nil || kickoff.Before(existing) {
			groups[index].StartUTC = kickoff.UTC().Format(time.RFC3339)
		}
		view := fixtureView{
			ID: game.ASAID, Kickoff: localKickoff.Format("Mon Jan 2, 3:04 PM MST"), KickoffUTC: kickoff.UTC().Format(time.RFC3339),
			HomeTeam: teams[game.HomeTeamID], AwayTeam: teams[game.AwayTeamID],
			Completed: game.Status == standings.CompletedStatus,
			Remaining: game.Status == remainingStatus,
			Status:    game.Status,
		}
		if outlook, ok := outlooks[game.ASAID]; ok && view.Remaining {
			view.Outlook = &outlook
		}
		if view.Completed && game.HomeScore.Valid && game.AwayScore.Valid {
			view.Score = fmt.Sprintf("%d–%d", game.HomeScore.Int64, game.AwayScore.Int64)
			if includeXG {
				if xg, ok := xgoals[game.ASAID]; ok {
					view.XG = fmt.Sprintf("%.2f–%.2f", xg.HomeXG.Float64, xg.AwayXG.Float64)
				}
			}
		}
		groups[index].Games = append(groups[index].Games, view)
	}
	return groups
}

// fixtureGroupsByStatus separates the fixtures-page views without splitting a
// matchday. A group joins Results as soon as it has a result; the browser also
// promotes a group once its first kickoff has passed, using the visitor's clock.
// Result matchdays are shown newest first; unfinished matchdays remain in
// kickoff order.
func fixtureGroupsByStatus(data cache.SeasonData, location *time.Location) (results, upcoming []fixtureGroupView) {
	return fixtureGroupsByStatusWithOutlooks(data, location, nil)
}

func fixtureGroupsByStatusWithOutlooks(data cache.SeasonData, location *time.Location, outlooks map[string]fixtureOutlookView) (results, upcoming []fixtureGroupView) {
	return fixtureGroupsByStatusWithOutlooksFor(data, location, outlooks, true)
}

func fixtureGroupsByStatusWithOutlooksFor(data cache.SeasonData, location *time.Location, outlooks map[string]fixtureOutlookView, includeXG bool) (results, upcoming []fixtureGroupView) {
	for _, group := range fixtureGroupsWithOutlooksFor(data, location, outlooks, includeXG) {
		hasResult := false
		hasUnfinished := false
		for _, game := range group.Games {
			if game.Completed {
				hasResult = true
			} else {
				hasUnfinished = true
			}
		}
		group.InProgress = hasResult && hasUnfinished
		if hasResult {
			results = append(results, group)
		} else {
			upcoming = append(upcoming, group)
		}
	}
	for left, right := 0, len(results)-1; left < right; left, right = left+1, right-1 {
		results[left], results[right] = results[right], results[left]
	}
	return results, upcoming
}

func fixtureTeams(teams []standings.Team) []teamNameView {
	values := make([]teamNameView, 0, len(teams))
	for _, team := range teams {
		values = append(values, teamName(team))
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].Name == values[j].Name {
			return values[i].ID < values[j].ID
		}
		return values[i].Name < values[j].Name
	})
	return values
}

func displayName(team standings.Team) string { return standings.DisplayName(team) }

func teamName(team standings.Team) teamNameView {
	return teamNameView{ID: team.ID, Name: displayName(team), LogoURL: clubLogoURL(team.ID)}
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

func scheduleDeltaText(value float64) string {
	return signedFloatText(math.Round(value*100) / 100)
}
