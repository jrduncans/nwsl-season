package app

import (
	"fmt"
	"math"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/history"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

type exploreHeadToHeadRow struct {
	exploreMatchRecord
	Season         string `json:"season"`
	Active         bool   `json:"active"`
	Result         string `json:"result"`
	Points         int    `json:"points"`
	ExpectedValues [3]string
	BasisScore     string
	PointsGap      string
	ChartGap       string
	ExpectedMargin string
	GoalMargin     string
	Margin         int64 `json:"margin"`
	// Position retains kickoff-instant order, including fixture-ID ties and undated last.
	Position int `json:"position"`
}

type exploreHeadToHeadPair struct {
	Opponent                   teamNameView `json:"opponent"`
	HomeExpected, AwayExpected history.HeadToHeadExpected
	Expected                   history.HeadToHeadExpected `json:"expected"`
	All                        history.HeadToHeadRecord   `json:"all"`
	Home                       history.HeadToHeadRecord   `json:"home"`
	Away                       history.HeadToHeadRecord   `json:"away"`
	Rows                       []exploreHeadToHeadRow     `json:"rows"`
	Scope                      string                     `json:"scope"`
	Warning                    string                     `json:"warning"`
}

// Each fixture is stored once in home orientation. Team and season indexes
// share identities and season-specific names. Both bases use the same fixtures.
type exploreHeadToHeadFixture struct {
	ID                   string      `json:"id"`
	Season               int         `json:"season"`
	Home                 int         `json:"home"`
	Away                 int         `json:"away"`
	Score                [2]int64    `json:"score"`
	XG                   [2]*float64 `json:"xg"`
	XPoints              [2]*float64 `json:"xpoints"`
	NonPenaltyGoals      *[2]int64   `json:"npGoals"`
	NonPenaltyXG         [2]*float64 `json:"npxg"`
	Date                 string      `json:"date"`
	Undated              bool        `json:"undated,omitempty"`
	VenueDateUnavailable bool        `json:"venueDateUnavailable,omitempty"`
}
type exploreHeadToHeadSeason struct {
	Year   string            `json:"year"`
	Active bool              `json:"active,omitempty"`
	Names  map[string]string `json:"names"`
}
type exploreHeadToHeadPayload struct {
	Teams    []teamNameView             `json:"teams"`
	Seasons  []exploreHeadToHeadSeason  `json:"seasons"`
	Fixtures []exploreHeadToHeadFixture `json:"fixtures"`
}
type exploreHeadToHeadView struct {
	H2HTeam                               teamNameView
	H2HTeamOptions                        []teamNameView
	H2HOpponents                          []exploreHeadToHeadPair
	H2HPair                               exploreHeadToHeadPair
	H2HRows                               []exploreHeadToHeadRow
	H2HData                               exploreHeadToHeadPayload
	H2HMarginTiles                        []exploreMarginTileView
	H2HSort, H2HOrder, H2HSeries          string
	H2HBoth, H2HMargins                   bool
	H2HPoints                             bool
	H2HPointsTiles                        []explorePointsTile
	H2HExpectedRows                       []exploreHeadToHeadComparison
	H2HCoverage, H2HXGLabel, H2HDirection string
	H2HExpanded                           bool
	H2HColumns                            []exploreTeamColumn
}

func exploreHeadToHead(query url.Values, summaries, npSummaries []history.SeasonScoring, archive []cache.HistoricalSeason, selected teamNameView, options []teamNameView) (exploreHeadToHeadView, error) {
	series, canonical, err := parseHeadToHeadSeries(query)
	if err != nil {
		return exploreHeadToHeadView{}, err
	}
	query = canonical
	page := exploreHeadToHeadView{H2HTeam: selected, H2HTeamOptions: options, H2HSeries: series, H2HSort: "date", H2HOrder: "desc", H2HExpanded: query.Has("h2h-sort") || query.Has("h2h-order")}
	for _, field := range []struct {
		key     string
		value   *string
		allowed []string
	}{
		{"h2h-sort", &page.H2HSort, []string{"date", "venue", "result", "margin"}},
		{"h2h-order", &page.H2HOrder, []string{"asc", "desc"}},
	} {
		if values, present := query[field.key]; present {
			if len(values) != 1 || !slices.Contains(field.allowed, values[0]) {
				return page, fmt.Errorf("invalid %s selection", field.key)
			}
			*field.value = values[0]
		}
	}
	// Build only the selected record on the server. In-place choices reuse a
	// compact archive of fixtures, independent of the carried penalties basis.
	basis, err := penaltiesBasis(query)
	if err != nil {
		return page, err
	}
	page.H2HPoints = page.H2HSeries == "points"
	page.H2HBoth = page.H2HSeries == "both"
	page.H2HMargins = page.H2HSeries == "actual"
	goals := summaries
	if basis.ExcludesPenalties() {
		goals = npSummaries
	}
	page.H2HXGLabel = basisText(basis, "xG")
	page.H2HDirection = h2hDirection(page.H2HSeries, basis)
	page.H2HData = headToHeadPayload(summaries, npSummaries, archive, options)
	for _, opponent := range history.HeadToHeadOpponentCounts(summaries, archive, selected.ID) {
		summary := history.HeadToHead(summaries, goals, archive, selected.ID, opponent.ID)
		pair := exploreHeadToHeadPair{Opponent: teamNameView{ID: opponent.ID, Name: opponent.Name, LogoURL: clubLogoURL(opponent.ID)}, Expected: summary.AllExpected, HomeExpected: summary.HomeExpected, AwayExpected: summary.AwayExpected, All: summary.All, Home: summary.Home, Away: summary.Away}
		for i, meeting := range summary.Meetings {
			record := exploreMatches([]history.TeamMatch{meeting.TeamMatch}, map[string]string{opponent.ID: meeting.OpponentName})[0]
			result := "Loss"
			switch meeting.Points {
			case 3:
				result = "Win"
			case 1:
				result = "Draw"
			}
			pair.Rows = append(pair.Rows, exploreHeadToHeadRow{exploreMatchRecord: record, Season: meeting.Season, Active: meeting.Active, Result: result, Points: meeting.Points, Margin: meeting.ScoreFor - meeting.ScoreAgainst, Position: i, BasisScore: h2hBasisScore(meeting), PointsGap: h2hPointsGap(meeting), ChartGap: h2hChartGap(meeting), ExpectedMargin: h2hExpectedMargin(meeting), GoalMargin: h2hGoalMargin(meeting), ExpectedValues: [3]string{h2hExpectedValue(meeting.XGFor), h2hExpectedValue(meeting.XGAgainst), h2hExpectedValue(meeting.XPoints)}})
		}
		first, last := "", ""
		active, venueDateUnavailable := false, false
		for _, row := range pair.Rows {
			if first == "" || row.Season < first {
				first = row.Season
			}
			if row.Season > last {
				last = row.Season
			}
			active = active || row.Active
			venueDateUnavailable = venueDateUnavailable || row.VenueDateUnavailable
		}
		years := first
		if first != last {
			years += "–" + last
		}
		pair.Scope = fmt.Sprintf("regular season, %s · %s", years, exploreMatchCount(pair.All.Played))
		if active {
			pair.Scope += " · season in progress"
		}
		if summary.Undated {
			pair.Warning = "Undated matches appear last."
		}
		if venueDateUnavailable {
			if pair.Warning != "" {
				pair.Warning += " "
			}
			pair.Warning += "Some venue-local dates are unavailable."
		}
		page.H2HOpponents = append(page.H2HOpponents, pair)
	}
	for _, pair := range page.H2HOpponents {
		// Pairs already order by newest name and ID, so ties retain that order.
		if pair.All.Played > page.H2HPair.All.Played {
			page.H2HPair = pair
		}
	}
	if values, present := query["opponent"]; present {
		if len(values) != 1 || values[0] == "" || values[0] == selected.ID {
			return page, fmt.Errorf("opponent must be one different recorded team")
		}
		index := slices.IndexFunc(options, func(t teamNameView) bool { return t.ID == values[0] })
		if index < 0 {
			return page, fmt.Errorf("opponent is not in the recorded regular-season results")
		}
		page.H2HPair = exploreHeadToHeadPair{Opponent: options[index], Scope: "regular season"}
		for _, pair := range page.H2HOpponents {
			if pair.Opponent.ID == values[0] {
				page.H2HPair = pair
			}
		}
		if page.H2HPair.All.Played == 0 {
			page.H2HOpponents = append(page.H2HOpponents, page.H2HPair)
		}
	}
	page.populateHeadToHeadTiles(basis)
	page.H2HRows = append([]exploreHeadToHeadRow(nil), page.H2HPair.Rows...)
	sortExploreHeadToHead(page.H2HRows, page.H2HSort, page.H2HOrder)
	page.H2HColumns = []exploreTeamColumn{{Key: "date", Label: "Date"}, {Label: "Season"}, {Key: "venue", Label: "Venue"}, {Label: "Opponent"}, {Key: "margin", Label: "Score", Description: "goal margin"}, {Key: "result", Label: "Result"}, {Label: "Points"}}
	for i := range page.H2HColumns {
		column := &page.H2HColumns[i]
		column.Sort = "none"
		if column.Key == "" {
			continue
		}
		order := "asc"
		if column.Key == page.H2HSort {
			column.Sort = "descending"
			if page.H2HOrder == "asc" {
				column.Sort, order = "ascending", "desc"
			}
		}
		target := cloneQuery(query)
		if target.Get("penalties") == "include" {
			target.Del("penalties")
		}
		target.Set("view", "head-to-head")
		target.Set("team", selected.ID)
		target.Set("opponent", page.H2HPair.Opponent.ID)
		target.Set("h2h-sort", column.Key)
		target.Set("h2h-order", order)
		column.URL = "?" + target.Encode()
	}
	return page, nil
}

func cloneQuery(query url.Values) url.Values {
	result := url.Values{}
	for key, values := range query {
		result[key] = append([]string(nil), values...)
	}
	return result
}

func sortExploreHeadToHead(rows []exploreHeadToHeadRow, column, order string) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		// Missing dates always follow dated results, including newest-first sorts.
		if a.Undated != b.Undated {
			return !a.Undated
		}
		cmp := a.Position - b.Position
		switch column {
		case "venue":
			cmp = strings.Compare(a.Venue, b.Venue)
		case "result":
			cmp = a.Points - b.Points
		case "margin":
			if a.Margin < b.Margin {
				cmp = -1
			} else if a.Margin > b.Margin {
				cmp = 1
			} else {
				cmp = 0
			}
		}
		if cmp == 0 {
			return a.Position > b.Position
		}
		if order == "asc" {
			return cmp < 0
		}
		return cmp > 0
	})
}

func headToHeadPayload(summaries, npSummaries []history.SeasonScoring, archive []cache.HistoricalSeason, teams []teamNameView) exploreHeadToHeadPayload {
	data := exploreHeadToHeadPayload{Teams: teams, Fixtures: []exploreHeadToHeadFixture{}}
	nonPenalty := map[string]map[string]history.TeamMatch{}
	for _, season := range npSummaries {
		matches := map[string]history.TeamMatch{}
		for _, team := range season.Teams {
			for _, match := range team.Matches {
				if match.Home {
					matches[match.ID] = match
				}
			}
		}
		nonPenalty[season.Season] = matches
	}
	indexes := map[string]int{}
	for i, team := range teams {
		indexes[team.ID] = i
	}
	// Keep kickoff strings only while sorting, not in the browser payload.
	type fixture struct {
		row     exploreHeadToHeadFixture
		kickoff string
	}
	var matches []fixture
	for _, season := range summaries {
		if !season.TeamComparisonEligible() {
			continue
		}
		info := exploreHeadToHeadSeason{Year: season.Season, Active: season.Lifecycle == cache.SourceScopeActive, Names: map[string]string{}}
		for _, input := range archive {
			if input.Entry.Season == season.Season {
				for _, team := range input.Data.Teams {
					info.Names[team.ID] = standings.DisplayName(team)
				}
			}
		}
		index := len(data.Seasons)
		data.Seasons = append(data.Seasons, info)
		awayPoints := map[string]*float64{}
		for _, team := range season.Teams {
			for _, match := range team.Matches {
				if !match.Home {
					awayPoints[match.ID] = match.XPoints
				}
			}
		}
		for _, team := range season.Teams {
			home, ok := indexes[team.TeamID]
			if !ok {
				continue
			}
			for _, match := range team.Matches {
				if !match.Home {
					continue
				}
				away, ok := indexes[match.OpponentID]
				if !ok {
					continue
				}
				record := exploreMatches([]history.TeamMatch{match}, nil)[0]
				row := exploreHeadToHeadFixture{ID: match.ID, Season: index, Home: home, Away: away, Score: [2]int64{match.ScoreFor, match.ScoreAgainst}, Date: record.Date, Undated: record.Undated, VenueDateUnavailable: record.VenueDateUnavailable}
				row.XG = [2]*float64{match.XGFor, match.XGAgainst}
				row.XPoints = [2]*float64{match.XPoints, awayPoints[match.ID]}
				np := nonPenalty[season.Season][match.ID]
				row.NonPenaltyXG = [2]*float64{np.XGFor, np.XGAgainst}
				if np.GoalsCovered {
					row.NonPenaltyGoals = &[2]int64{np.GoalsFor, np.GoalsAgainst}
				}
				matches = append(matches, fixture{row: row, kickoff: match.KickoffUTC})
			}
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		a, ae := fixtures.ParseKickoff(matches[i].kickoff)
		b, be := fixtures.ParseKickoff(matches[j].kickoff)
		if (ae == nil) != (be == nil) {
			return ae == nil
		}
		if ae == nil && !a.Equal(b) {
			return a.Before(b)
		}
		return matches[i].row.ID < matches[j].row.ID
	})
	for _, match := range matches {
		data.Fixtures = append(data.Fixtures, match.row)
	}
	return data
}

// These strings are shared by the exact values table and the no-script chart.
func h2hExpectedValue(value *float64) string {
	if value == nil {
		return "Unavailable"
	}
	return fmt.Sprintf("%.2f", *value)
}

type exploreHeadToHeadComparison struct {
	Label, ExpectedLabel, Actual, Expected, Gap, Rate, Coverage string
}

func h2hComparisons(pair exploreHeadToHeadPair, basis history.ScoringBasis) []exploreHeadToHeadComparison {
	e := pair.Expected
	var rows []exploreHeadToHeadComparison
	for _, metric := range []struct {
		label, expectedLabel string
		actual               float64
		expected             *float64
		count                int
		missing              string
	}{
		{basisText(basis, "Goals for"), basisText(basis, "xG for"), float64(e.GoalsFor), e.XGFor, e.GoalsCovered, basisText(basis, "xG incomplete")},
		{basisText(basis, "Goals against"), basisText(basis, "xG against"), float64(e.GoalsAgainst), e.XGAgainst, e.GoalsCovered, basisText(basis, "xG incomplete")},
		{"Points", "xPts", float64(pair.All.Points), e.XPoints, pair.All.Played, "xPts incomplete"},
	} {
		r := exploreHeadToHeadComparison{Label: metric.label, ExpectedLabel: metric.expectedLabel, Actual: "Unavailable", Expected: metric.missing, Gap: metric.missing, Rate: "Unavailable", Coverage: fmt.Sprintf("%d of %d meetings", metric.count, pair.All.Played)}
		if metric.count > 0 {
			r.Actual = strconv.FormatInt(int64(metric.actual), 10)
			r.Rate = fmt.Sprintf("%.2f per meeting", metric.actual/float64(metric.count))
			if metric.expected != nil {
				r.Expected = h2hExpectedValue(metric.expected)
				gap := metric.actual - *metric.expected
				direction := "above expected"
				if gap < 0 {
					direction = "below expected"
					gap = -gap
				}
				r.Gap = fmt.Sprintf("%.2f %s", gap, direction)
				r.Rate = fmt.Sprintf("%.2f vs %.2f per meeting", metric.actual/float64(metric.count), *metric.expected/float64(metric.count))
			}
		}
		rows = append(rows, r)
	}
	return rows
}

func h2hCoverage(pair exploreHeadToHeadPair, basis history.ScoringBasis, series string) string {
	if series == "points" {
		return ""
	}
	var notes []string
	if n := pair.All.Played - pair.Expected.XGCovered; n > 0 {
		notes = append(notes, h2hMissingNote(n, basisText(basis, "xG")))
	}
	if n := pair.All.Played - pair.Expected.XPointsCovered; series == "both" && n > 0 {
		notes = append(notes, h2hMissingNote(n, "xPts"))
	}
	if basis.ExcludesPenalties() && pair.Expected.GoalsCovered < pair.All.Played {
		notes = append(notes, fmt.Sprintf("Non-penalty goals and npxG cover %d of %d meetings with a penalty split.", pair.Expected.GoalsCovered, pair.All.Played))
	}
	return strings.Join(notes, " ")
}

func h2hBasisScore(meeting history.HeadToHeadMeeting) string {
	if !meeting.GoalsCovered {
		return "Unavailable"
	}
	return fmt.Sprintf("%d–%d", meeting.GoalsFor, meeting.GoalsAgainst)
}

func h2hMissingNote(n int, metric string) string {
	if n == 1 {
		return fmt.Sprintf("1 meeting lacks %s.", metric)
	}
	return fmt.Sprintf("%d meetings lack %s.", n, metric)
}

func h2hDirection(series string, basis history.ScoringBasis) string {
	if series == "points" {
		return "Above zero: more points than xPoints. Below: fewer."
	}
	if series == "both" {
		return basisText(basis, "Goal margin − xG margin · zero: matched expected")
	}
	if basis.ExcludesPenalties() {
		return "zero: level"
	}
	return "up: win · zero: draw · down: loss"
}

func h2hPointsGap(meeting history.HeadToHeadMeeting) string {
	if meeting.XPoints == nil {
		return "Unavailable"
	}
	return h2hSignedMargin(float64(meeting.Points) - *meeting.XPoints)
}

func h2hChartGap(meeting history.HeadToHeadMeeting) string {
	if !meeting.GoalsCovered || meeting.XGFor == nil || meeting.XGAgainst == nil {
		return "Unavailable"
	}
	gap := float64(meeting.GoalsFor) - float64(meeting.GoalsAgainst) - (*meeting.XGFor - *meeting.XGAgainst)
	return h2hSignedMargin(gap)
}

func h2hExpectedMargin(meeting history.HeadToHeadMeeting) string {
	if meeting.XGFor == nil || meeting.XGAgainst == nil {
		return "Unavailable"
	}
	return h2hSignedMargin(*meeting.XGFor - *meeting.XGAgainst)
}

func h2hSignedMargin(gap float64) string {
	if math.Abs(gap) < .005 {
		gap = 0
	}
	if gap >= .005 {
		return fmt.Sprintf("+%.2f", gap)
	}
	return fmt.Sprintf("%.2f", gap)
}

func h2hGoalMargin(meeting history.HeadToHeadMeeting) string {
	if !meeting.GoalsCovered {
		return "Unavailable"
	}
	margin := meeting.GoalsFor - meeting.GoalsAgainst
	if margin > 0 {
		return fmt.Sprintf("+%d", margin)
	}
	return fmt.Sprint(margin)
}

// parseHeadToHeadSeries validates once and returns canonical link state.
// expected is a legacy spelling of the default Margins view, actual.
func parseHeadToHeadSeries(query url.Values) (string, url.Values, error) {
	series := "actual"
	if values, present := query["h2h-series"]; present {
		if len(values) != 1 || !slices.Contains([]string{"actual", "expected", "both", "points"}, values[0]) {
			return "", nil, fmt.Errorf("invalid h2h-series selection")
		}
		series = values[0]
	}
	if series == "expected" {
		series = "actual"
	}
	canonical := cloneQuery(query)
	if series == "actual" {
		canonical.Del("h2h-series")
	} else {
		canonical.Set("h2h-series", series)
	}
	return series, canonical, nil
}

func (page *exploreHeadToHeadView) populateHeadToHeadTiles(basis history.ScoringBasis) {
	matches := make([]exploreMatchRecord, 0, len(page.H2HPair.Rows))
	for _, row := range page.H2HPair.Rows {
		matches = append(matches, row.exploreMatchRecord)
	}
	page.H2HMarginTiles = formatMarginTiles(exploreMarginTiles(matches), basis)
	page.H2HPointsTiles = explorePointsTiles(matches, "meeting")
	page.H2HExpectedRows = h2hComparisons(page.H2HPair, basis)
	page.H2HCoverage = h2hCoverage(page.H2HPair, basis, page.H2HSeries)
}
