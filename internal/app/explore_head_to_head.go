package app

import (
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strings"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/history"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

type exploreHeadToHeadRow struct {
	exploreMatchRecord
	Season string `json:"season"`
	Active bool   `json:"active"`
	Result string `json:"result"`
	Points int    `json:"points"`
	Margin int64  `json:"margin"`
	// Position retains kickoff-instant order, including fixture-ID ties and undated last.
	Position int `json:"position"`
}

type exploreHeadToHeadPair struct {
	Opponent teamNameView             `json:"opponent"`
	All      history.HeadToHeadRecord `json:"all"`
	Home     history.HeadToHeadRecord `json:"home"`
	Away     history.HeadToHeadRecord `json:"away"`
	Rows     []exploreHeadToHeadRow   `json:"rows"`
	Scope    string                   `json:"scope"`
	Warning  string                   `json:"warning"`
}

// Each fixture is stored once in home orientation. Team and season indexes
// share identities and season-specific names; no expected values enter this payload.
type exploreHeadToHeadFixture struct {
	ID                   string   `json:"id"`
	Season               int      `json:"season"`
	Home                 int      `json:"home"`
	Away                 int      `json:"away"`
	Score                [2]int64 `json:"score"`
	Date                 string   `json:"date"`
	Undated              bool     `json:"undated,omitempty"`
	VenueDateUnavailable bool     `json:"venueDateUnavailable,omitempty"`
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
type exploreHeadToHeadRecordRow struct {
	Label string
	history.HeadToHeadRecord
	Rate string
}

type exploreHeadToHeadView struct {
	H2HTeam           teamNameView
	H2HTeamOptions    []teamNameView
	H2HOpponents      []exploreHeadToHeadPair
	H2HPair           exploreHeadToHeadPair
	H2HRows           []exploreHeadToHeadRow
	H2HData           exploreHeadToHeadPayload
	H2HRecordRows     []exploreHeadToHeadRecordRow
	H2HSort, H2HOrder string
	H2HExpanded       bool
	H2HColumns        []exploreTeamColumn
}

func exploreHeadToHead(query url.Values, summaries []history.SeasonScoring, archive []cache.HistoricalSeason, selected teamNameView, options []teamNameView) (exploreHeadToHeadView, error) {
	page := exploreHeadToHeadView{H2HTeam: selected, H2HTeamOptions: options, H2HSort: "date", H2HOrder: "desc", H2HExpanded: query.Has("h2h-sort") || query.Has("h2h-order")}
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
	page.H2HData = headToHeadPayload(summaries, archive, options)
	for _, opponent := range history.HeadToHeadOpponentCounts(summaries, archive, selected.ID) {
		summary := history.HeadToHead(summaries, archive, selected.ID, opponent.ID)
		pair := exploreHeadToHeadPair{Opponent: teamNameView{ID: opponent.ID, Name: opponent.Name, LogoURL: clubLogoURL(opponent.ID)}, All: summary.All, Home: summary.Home, Away: summary.Away}
		for i, meeting := range summary.Meetings {
			record := exploreMatches([]history.TeamMatch{meeting.TeamMatch}, map[string]string{opponent.ID: meeting.OpponentName})[0]
			result := "Loss"
			switch meeting.Points {
			case 3:
				result = "Win"
			case 1:
				result = "Draw"
			}
			pair.Rows = append(pair.Rows, exploreHeadToHeadRow{exploreMatchRecord: record, Season: meeting.Season, Active: meeting.Active, Result: result, Points: meeting.Points, Margin: meeting.ScoreFor - meeting.ScoreAgainst, Position: i})
		}
		first, last := "", ""
		active, venueUnknown := false, false
		for _, row := range pair.Rows {
			if first == "" || row.Season < first {
				first = row.Season
			}
			if row.Season > last {
				last = row.Season
			}
			active = active || row.Active
			venueUnknown = venueUnknown || row.VenueDateUnavailable
		}
		years := first
		if first != last {
			years += "–" + last
		}
		pair.Scope = fmt.Sprintf("regular season, %s · %s", years, h2hMatchCount(pair.All.Played))
		if active {
			pair.Scope += " · season in progress"
		}
		if summary.Undated {
			pair.Warning = "Undated matches appear last."
		}
		if venueUnknown {
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
	for _, row := range []struct {
		label  string
		record history.HeadToHeadRecord
	}{{"All", page.H2HPair.All}, {"Home", page.H2HPair.Home}, {"Away", page.H2HPair.Away}} {
		rate := "—"
		if row.record.Played > 0 {
			rate = fmt.Sprintf("%.2f", float64(row.record.Points)/float64(row.record.Played))
		}
		page.H2HRecordRows = append(page.H2HRecordRows, exploreHeadToHeadRecordRow{Label: row.label, HeadToHeadRecord: row.record, Rate: rate})
	}
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

func h2hMatchCount(n int) string {
	if n == 1 {
		return "1 match"
	}
	return fmt.Sprintf("%d matches", n)
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

func headToHeadPayload(summaries []history.SeasonScoring, archive []cache.HistoricalSeason, teams []teamNameView) exploreHeadToHeadPayload {
	data := exploreHeadToHeadPayload{Teams: teams, Fixtures: []exploreHeadToHeadFixture{}}
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
