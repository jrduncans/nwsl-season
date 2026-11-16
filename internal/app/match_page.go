package app

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"

	"github.com/jrduncans/nwsl-season/internal/bracket"
	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/forecast"
	"github.com/jrduncans/nwsl-season/internal/scheduleload"
	"github.com/jrduncans/nwsl-season/internal/simulation"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

// matchPage compares the two teams in one match as of its kickoff and, where
// the season has a forecast, explains the default model's match outlook.
type matchPage struct {
	seasonPage
	Match   fixtureView
	Playoff bool
	// Final marks the designated-site Championship. VenueNote names its
	// actual venue, and VenueControl lets the reader choose the venue the
	// outlook assumes.
	Final        bool
	VenueNote    string
	VenueControl *matchVenueControl
	RoundLabel   string
	HomeCode     string
	AwayCode     string
	// Lines sit under the score: xG, npxG and predicted goals, each with
	// the home value, a label, then the away value.
	Lines          []matchLine
	Rest           string
	Sides          []matchSideView
	ModelName      string
	Ranks          *matchRanksView
	HeadToHeadPath string
}

// matchVenueControl chooses whether the Championship outlook treats the
// match as neutral or as a home match for either team. Values are "neutral",
// "home" and "away", oriented like the page.
type matchVenueControl struct {
	Options []exploreSegment
}

type matchLine struct {
	Label, Title, Home, Away string
}

// matchSideView lists the factors whose product is one side's predicted
// goals (the model's scoring rate; "expected goals" would read as xG).
type matchSideView struct {
	Team      teamNameView
	Predicted string
	Rows      []matchFactorView
}

type matchFactorView struct {
	Label, Detail, Value string
}

type matchRanksView struct {
	Scope string
	// Groups pair each xG ranking with its goals counterpart.
	Groups     []matchRankGroup
	Home, Away matchRankTeamView
	Rows       []matchRankRowView
	TeamCount  int
	MissingXG  bool
}

type matchRankGroup struct {
	Label     string
	XG, Goals matchRankRowView
}

type matchRankTeamView struct {
	Team   teamNameView
	Code   string
	Played int
}

type matchRankRowView struct {
	Label      string
	Home, Away matchRankCellView
	// Better is "home" or "away" when one team ranks strictly higher.
	Better string
}

type matchRankCellView struct {
	Value, Rank string
	// Place is the competition rank, or zero when withheld.
	Place int
}

// matchPagesAvailable limits match pages to regular-season and playoff
// matches, the two competitions the match outlook covers.
func matchPagesAvailable(scope requestCompetition) bool {
	return scope.Cataloged && scope.fixturesAvailable() && (scope.Stage == "Regular Season" || scope.Stage == "Playoffs")
}

func matchPathPrefix(from string, scope requestCompetition) string {
	if !matchPagesAvailable(scope) {
		return ""
	}
	return relativeURL(from, stageURL(scope.Season, scope.Entry.Slug)+"/matches")
}

func matchPath(prefix, id string) string {
	if prefix == "" || id == "" {
		return ""
	}
	return prefix + "/" + url.PathEscape(id)
}

func (a *application) match(w http.ResponseWriter, r *http.Request) {
	scope := a.requestScope(r)
	if !scope.Valid || !matchPagesAvailable(scope) {
		http.NotFound(w, r)
		return
	}
	data, err := a.loadSeasonData(r.Context(), scope.Season, scope.Stage)
	if err != nil {
		a.renderError(w, r, err)
		return
	}
	var game cache.Game
	found := false
	for _, candidate := range data.Games {
		if candidate.ASAID == r.PathValue("id") {
			game, found = candidate, true
			break
		}
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	page, err := a.loadSeasonPage(r)
	if err != nil {
		a.renderError(w, r, err)
		return
	}
	view, err := a.matchView(r.Context(), scope, data, game, r.URL.Query().Get("venue"))
	if err != nil {
		a.renderError(w, r, err)
		return
	}
	view.seasonPage = page
	// The match is already chosen, so switching season or competition has no destination.
	view.SeasonSelector, view.StageSelector = nil, nil
	view.Title = view.Match.HomeTeam.Name + " vs " + view.Match.AwayTeam.Name
	view.HeadToHeadPath = relativeURL(r.URL.Path, "/explore") + "?" + url.Values{"view": {"head-to-head"}, "team": {game.HomeTeamID}, "opponent": {game.AwayTeamID}}.Encode()
	a.render(w, "match", view)
}

// matchView builds the page. For the Championship, venue chooses the venue
// the outlook assumes ("neutral", "home" or "away"); anything else uses the
// actual venue.
func (a *application) matchView(ctx context.Context, scope requestCompetition, data cache.SeasonData, game cache.Game, venue string) (matchPage, error) {
	var page matchPage
	for _, group := range fixtureGroupsWithOutlooksFor(data, a.options.Location, nil, scope.xgAvailable()) {
		for _, view := range group.Games {
			if view.ID == game.ASAID {
				page.Match = view
			}
		}
	}
	training, forecastReady := data, scope.forecastCapabilityAvailable()
	games := matchTrainingGames(standingsGames(data.Games), game.ASAID)
	if scope.Stage == "Playoffs" {
		page.Playoff = true
		regular, err := a.loadSeasonData(ctx, scope.Season, "Regular Season")
		if err != nil {
			return matchPage{}, fmt.Errorf("load %s regular season: %w", scope.Season, err)
		}
		training, games = regular, standingsGames(regular.Games)
		entry, _ := competition.Lookup(scope.Season, "Regular Season")
		rules, verified := a.rulesForSeason(scope.Season, "Regular Season")
		forecastReady = requestCompetition{Season: scope.Season, Stage: "Regular Season", Entry: entry, Cataloged: true}.forecastAvailable(rules, verified)
		page.RoundLabel, page.Final = playoffRound(scope.Entry, data.Games, playoffSeedsFrom(scope.Season, regular), game.ASAID)
	}
	// host is the team the outlook treats as at home in the Championship, or
	// "" for a neutral one. Other matches keep ASA's home designation.
	host, actual := "", ""
	if page.Final {
		host = championshipHost(training.Games, game)
		page.VenueNote, actual = "Neutral venue", "neutral"
		switch host {
		case game.HomeTeamID:
			page.VenueNote, actual = "At "+page.Match.HomeTeam.Name+"'s stadium", "home"
		case game.AwayTeamID:
			page.VenueNote, actual = "At "+page.Match.AwayTeam.Name+"'s stadium", "away"
		}
		switch venue {
		case "neutral":
			host = ""
		case "home":
			host = game.HomeTeamID
		case "away":
			host = game.AwayTeamID
		default:
			venue = actual
		}
	}
	codes := map[string]string{}
	for _, team := range training.Teams {
		codes[team.ID] = scenarioTeamCode(team)
	}
	for _, team := range data.Teams {
		codes[team.ID] = scenarioTeamCode(team)
	}
	page.HomeCode, page.AwayCode = codes[game.HomeTeamID], codes[game.AwayTeamID]
	// whatIf names a chosen venue that is not the actual one, for the outlook
	// label, so a shared link or screenshot never passes it off as real.
	whatIf := ""
	if page.Final {
		options := exploreSegments(venue,
			exploreSegment{Value: "neutral", Label: "Neutral"},
			exploreSegment{Value: "home", Label: page.HomeCode + " home"},
			exploreSegment{Value: "away", Label: page.AwayCode + " home"})
		for i := range options {
			if options[i].Value == actual {
				options[i].Label += " (actual)"
			}
			if options[i].Selected && venue != actual {
				whatIf = "if " + options[i].Label // "if neutral" reads as a phrase.
				if venue == "neutral" {
					whatIf = "if neutral"
				}
			}
		}
		page.VenueControl = &matchVenueControl{Options: options}
	}
	target := standings.Game{ID: game.ASAID, Status: fixtures.PreMatchStatus, HomeTeamID: game.HomeTeamID, AwayTeamID: game.AwayTeamID}
	if kickoff, err := fixtures.ParseKickoff(game.KickoffUTC); err == nil {
		target.Kickoff = kickoff
	}
	rest := ""
	if !page.Playoff {
		rest = restDetail(games, target, codes)
	}
	page.Rest = strings.TrimSuffix(rest, ".")
	xgoals := forecastXGoals(training)
	stats := matchTeamStats(training.Teams, games, xgoals)
	// Before a team's first match there is nothing of its own to rank.
	if stats[game.HomeTeamID].played > 0 && stats[game.AwayTeamID].played > 0 {
		page.Ranks = matchRanks(stats, game.HomeTeamID, game.AwayTeamID, page.Match, codes)
		page.Ranks.Scope = fmt.Sprintf("Before kickoff, among %d teams", page.Ranks.TeamCount)
		if page.Playoff {
			page.Ranks.Scope = fmt.Sprintf("Regular season, among %d teams", page.Ranks.TeamCount)
		}
	}

	var predictedLine *matchLine
	var outlook fixtureOutlookView
	var breakdown forecast.Breakdown
	predicted := false
	if forecastReady {
		model := forecast.Default().Model
		input := forecast.FitInput{Teams: training.Teams, Games: games, XGoals: xgoals, HistoricalVenue: forecastVenueSample(training)}
		// An away team hosting the Championship is predicted as the home side,
		// then turned back to the page's orientation.
		flipped := page.Final && host == game.AwayTeamID
		predictionTarget := target
		if flipped {
			predictionTarget.HomeTeamID, predictionTarget.AwayTeamID = target.AwayTeamID, target.HomeTeamID
		}
		outlook, breakdown, predicted = matchPrediction(model, input, predictionTarget, page.Playoff, page.Final && host == "")
		if flipped {
			outlook = outlook.reversed()
			breakdown.Home, breakdown.Away = breakdown.Away, breakdown.Home
		}
		if predicted {
			if page.Match.Completed {
				outlook.Label = "Before kickoff"
			}
			if whatIf != "" {
				outlook.Label = "If" + whatIf[2:]
				if page.Match.Completed {
					outlook.Label = "Before kickoff, " + whatIf
				}
			}
			page.Match.Outlook = &outlook
			page.ModelName = model.Info().Name
			predictedLine = &matchLine{Label: "Predicted", Home: fmt.Sprintf("%.2f", breakdown.Home.Expected), Away: fmt.Sprintf("%.2f", breakdown.Away.Expected)}
			home := matchSide(page.Match.HomeTeam, page.HomeCode, page.AwayCode, !flipped, breakdown.Home, breakdown.Neutral, stats[game.HomeTeamID], stats[game.AwayTeamID], rest)
			away := matchSide(page.Match.AwayTeam, page.AwayCode, page.HomeCode, flipped, breakdown.Away, breakdown.Neutral, stats[game.AwayTeamID], stats[game.HomeTeamID], rest)
			page.Sides = []matchSideView{home, away}
		}
	}
	if !predicted {
		page.VenueControl = nil // Nothing on the page depends on the venue.
	}
	if page.Match.Completed && scope.xgAvailable() {
		if xg, ok := forecastXGoals(data)[game.ASAID]; ok {
			page.Lines = append(page.Lines, matchLine{Label: "xG", Home: fmt.Sprintf("%.2f", xg.Home), Away: fmt.Sprintf("%.2f", xg.Away)})
			if split := xg.Penalties; split != nil && (split.Home > 0 || split.Away > 0) {
				page.Lines = append(page.Lines, matchLine{Label: "npxG", Title: "Non-penalty expected goals", Home: fmt.Sprintf("%.2f", math.Max(0, xg.Home-split.Home)), Away: fmt.Sprintf("%.2f", math.Max(0, xg.Away-split.Away))})
			}
		}
	}
	if predictedLine != nil {
		page.Lines = append(page.Lines, *predictedLine)
	}
	return page, nil
}

// matchPrediction fits the model and explains the target's rates. Like the
// Results outlook, a failure leaves the page without a prediction rather
// than failing it.
func matchPrediction(model forecast.Model, input forecast.FitInput, target standings.Game, playoff, neutral bool) (fixtureOutlookView, forecast.Breakdown, bool) {
	predictor, fitErr := model.Fit(input)
	if fitErr != nil {
		return fixtureOutlookView{}, forecast.Breakdown{}, false
	}
	explainer, ok := predictor.(forecast.Explainer)
	if !ok {
		return fixtureOutlookView{}, forecast.Breakdown{}, false
	}
	outlook := fixtureOutlookView{ModelName: model.Info().Name, Playoff: playoff}
	var breakdown forecast.Breakdown
	var outcomes forecast.OutcomeProbabilities
	var explainErr error
	if playoff {
		odds, oddsErr := simulation.PlayoffMatchOutlook(predictor, target.HomeTeamID, target.AwayTeamID, neutral)
		if oddsErr != nil {
			return fixtureOutlookView{}, forecast.Breakdown{}, false
		}
		outcomes = odds.OutcomeProbabilities
		outlook.HomeAdvanceText, outlook.AwayAdvanceText = percent(odds.HomeAdvance), percent(odds.AwayAdvance)
		target.ID = "playoff:" + target.HomeTeamID + ":" + target.AwayTeamID
		breakdown, explainErr = explainer.PostseasonBreakdown(target, neutral)
	} else {
		distribution, distributionErr := predictor.Distribution(target)
		if distributionErr != nil {
			return fixtureOutlookView{}, forecast.Breakdown{}, false
		}
		outcomes = distribution.Outcomes()
		breakdown, explainErr = explainer.Breakdown(target)
	}
	if explainErr != nil || !validFixtureOutcomes(outcomes) {
		return fixtureOutlookView{}, forecast.Breakdown{}, false
	}
	outlook.HomeWin, outlook.Draw, outlook.AwayWin = outcomes.HomeWin, outcomes.Draw, outcomes.AwayWin
	outlook.HomeWinText, outlook.DrawText, outlook.AwayWinText = percent(outlook.HomeWin), percent(outlook.Draw), percent(outlook.AwayWin)
	return outlook, breakdown, true
}

// matchTrainingGames turns the target match, and for a completed target every
// match kicking off at or after it, back into unplayed fixtures. Upcoming
// targets keep every completed result, so their outlook matches Results.
func matchTrainingGames(games []standings.Game, id string) []standings.Game {
	var target standings.Game
	for _, game := range games {
		if game.ID == id {
			target = game
		}
	}
	values := make([]standings.Game, len(games))
	for i, game := range games {
		if game.ID == id || (target.Status == standings.CompletedStatus && game.Status == standings.CompletedStatus && !game.Kickoff.Before(target.Kickoff)) {
			game.Status = fixtures.PreMatchStatus
			game.HomeScore, game.AwayScore = nil, nil
		}
		values[i] = game
	}
	return values
}

// playoffRound names the bracket round holding a published playoff match and
// reports whether it is the neutral-venue final.
func playoffRound(entry competition.Entry, games []cache.Game, seeds playoffSeeds, id string) (string, bool) {
	if entry.BracketFormat == nil {
		return "", false
	}
	// Place games as the bracket page and playoff outlooks do.
	view := bracket.Build(*entry.BracketFormat, games)
	if seeds.Certified {
		view = bracket.BuildWithSeeds(*entry.BracketFormat, games, seeds.Seeds)
	}
	for ri, round := range view.Rounds {
		for _, slot := range round.Slots {
			if slot.Game != nil && slot.Game.ASAID == id {
				return round.Label, ri == len(view.Rounds)-1
			}
		}
	}
	return "", false
}

type matchTeamStat struct {
	played, xgPlayed       int
	goalsFor, goalsAgainst int
	xgFor, xgAgainst       float64
}

func (s matchTeamStat) perMatch() [6]*float64 {
	var values [6]*float64
	n := float64(s.played)
	gf, ga, gd := float64(s.goalsFor)/n, float64(s.goalsAgainst)/n, float64(s.goalsFor-s.goalsAgainst)/n
	values[0], values[1], values[2] = &gf, &ga, &gd
	if s.xgPlayed == s.played {
		xf, xa, xd := s.xgFor/n, s.xgAgainst/n, (s.xgFor-s.xgAgainst)/n
		values[3], values[4], values[5] = &xf, &xa, &xd
	}
	return values
}

// matchTeamStats totals every team with a completed match in games. xG
// counts only matches with an observation, as the model's fit does.
func matchTeamStats(teams []standings.Team, games []standings.Game, xgoals map[string]forecast.ExpectedGoals) map[string]matchTeamStat {
	known := map[string]bool{}
	for _, team := range teams {
		known[team.ID] = true
	}
	stats := map[string]matchTeamStat{}
	for _, game := range games {
		if game.Status != standings.CompletedStatus || game.HomeScore == nil || game.AwayScore == nil || !known[game.HomeTeamID] || !known[game.AwayTeamID] {
			continue
		}
		home, away := stats[game.HomeTeamID], stats[game.AwayTeamID]
		home.played++
		away.played++
		home.goalsFor += *game.HomeScore
		home.goalsAgainst += *game.AwayScore
		away.goalsFor += *game.AwayScore
		away.goalsAgainst += *game.HomeScore
		if xg, ok := xgoals[game.ID]; ok {
			home.xgPlayed++
			away.xgPlayed++
			home.xgFor += xg.Home
			home.xgAgainst += xg.Away
			away.xgFor += xg.Away
			away.xgAgainst += xg.Home
		}
		stats[game.HomeTeamID], stats[game.AwayTeamID] = home, away
	}
	return stats
}

var matchRankLabels = [6]string{"Goals scored", "Goals allowed", "Goal differential", "xG", "xG allowed", "xG differential"}

// matchRanks follows Explore's Rankings rules: per-match competition ranks,
// lower is better for allowed stats, and xG ranks withheld for every team
// when any team's xG coverage is incomplete.
func matchRanks(stats map[string]matchTeamStat, homeID, awayID string, match fixtureView, codes map[string]string) *matchRanksView {
	ids := make([]string, 0, len(stats))
	for id := range stats {
		ids = append(ids, id)
	}
	values := make([][6]*float64, len(ids))
	complete := true
	for i, id := range ids {
		values[i] = stats[id].perMatch()
		complete = complete && values[i][3] != nil
	}
	view := &matchRanksView{
		Home:      matchRankTeamView{Team: match.HomeTeam, Code: codes[homeID], Played: stats[homeID].played},
		Away:      matchRankTeamView{Team: match.AwayTeam, Code: codes[awayID], Played: stats[awayID].played},
		TeamCount: len(ids),
		MissingXG: !complete,
	}
	cell := func(teamID string, measure int) matchRankCellView {
		index := -1
		for i, id := range ids {
			if id == teamID {
				index = i
			}
		}
		if index < 0 {
			return matchRankCellView{Value: "—"}
		}
		value := values[index][measure]
		if value == nil {
			return matchRankCellView{Value: "—"}
		}
		result := matchRankCellView{Value: fmt.Sprintf("%.2f", *value)}
		if measure == 2 || measure == 5 {
			result.Value = signedFloatText(*value)
		}
		if measure < 3 || complete {
			present := make([]float64, len(ids))
			for i := range ids {
				present[i] = *values[i][measure]
			}
			place, tied := competitionRank(present, index, measure == 1 || measure == 4)
			result.Rank, result.Place = competitionRankText(place, tied), place
		}
		return result
	}
	for measure, label := range matchRankLabels {
		row := matchRankRowView{Label: label, Home: cell(homeID, measure), Away: cell(awayID, measure)}
		if row.Home.Place > 0 && row.Away.Place > 0 {
			switch {
			case row.Home.Place < row.Away.Place:
				row.Better = "home"
			case row.Away.Place < row.Home.Place:
				row.Better = "away"
			}
		}
		view.Rows = append(view.Rows, row)
	}
	for i, label := range []string{"Scored", "Allowed", "Differential"} {
		xg, goals := view.Rows[i+3], view.Rows[i]
		xg.Label, goals.Label = "xG", "Goals"
		view.Groups = append(view.Groups, matchRankGroup{Label: label, XG: xg, Goals: goals})
	}
	return view
}

func matchSide(team teamNameView, code, opponentCode string, home bool, factors forecast.RateFactors, neutral bool, own, opponent matchTeamStat, rest string) matchSideView {
	venue, venueDetail := "League away rate", "League xG per match away from home"
	if home {
		venue, venueDetail = "League home rate", "League xG per match at home"
	}
	if neutral {
		venue, venueDetail = "League neutral rate", "Average of the home and away rates"
	}
	detail := func(xg float64, played, matches int, noun string) string {
		if matches == 0 {
			return "No matches yet"
		}
		if played == 0 {
			return "No xG yet"
		}
		return fmt.Sprintf("%s %.2f per match", noun, xg/float64(played))
	}
	value := func(v float64) string { return fmt.Sprintf("%.2f", v) }
	rows := []matchFactorView{
		{Label: venue, Detail: venueDetail, Value: value(factors.Venue)},
		{Label: "× " + code + " attack", Detail: detail(own.xgFor, own.xgPlayed, own.played, "xG"), Value: value(factors.Attack)},
		{Label: "× " + opponentCode + " defense", Detail: detail(opponent.xgAgainst, opponent.xgPlayed, opponent.played, "xG allowed"), Value: value(factors.Defense)},
	}
	// Equal load cancels exactly, leaving nothing to show.
	if factors.Load != 1 {
		rows = append(rows, matchFactorView{Label: "× Rest", Detail: strings.TrimSuffix(rest, "."), Value: value(factors.Load)})
	}
	if factors.Clamped {
		rows = append(rows, matchFactorView{Label: "Rate bound", Detail: "Limited to 0.20–4.50 goals", Value: value(factors.Expected)})
	}
	return matchSideView{Team: team, Predicted: value(factors.Expected), Rows: rows}
}

// restDetail names each team's schedule load entering the match, using the
// same kickoffs as the model's schedule-load context.
func restDetail(games []standings.Game, target standings.Game, codes map[string]string) string {
	loads, err := scheduleload.Calculate(games)
	if err != nil {
		return ""
	}
	load := loads[target.ID]
	describe := func(teamID string, team scheduleload.Team) string {
		if team.Recovery == 0 && !team.ThirdWithinNine {
			return ""
		}
		var previous []standings.Game
		for _, game := range games {
			if (game.Status == standings.CompletedStatus || game.Status == fixtures.PreMatchStatus) && game.Kickoff.Before(target.Kickoff) && (game.HomeTeamID == teamID || game.AwayTeamID == teamID) {
				previous = append(previous, game)
			}
		}
		text := codes[teamID]
		if team.Recovery > 0 {
			var last standings.Game
			for _, game := range previous {
				if game.Kickoff.After(last.Kickoff) {
					last = game
				}
			}
			days := target.Kickoff.Sub(last.Kickoff).Hours() / 24
			if whole := math.Floor(days); whole == 1 {
				text += " on 1 day's rest"
			} else {
				text += fmt.Sprintf(" on %.0f days' rest", whole)
			}
		}
		if team.ThirdWithinNine {
			if team.Recovery > 0 {
				text += ","
			}
			text += " third match in 9 days"
		}
		return text
	}
	parts := []string{}
	for _, part := range []string{describe(target.HomeTeamID, load.Home), describe(target.AwayTeamID, load.Away)} {
		if part != "" {
			parts = append(parts, part)
		}
	}
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0] + "."
	}
	return parts[0] + "; " + parts[1] + "."
}
