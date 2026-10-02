package app

import (
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/history"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

func TestExploreSeasonTrendChronologyVenueAndPartialXG(t *testing.T) {
	archive := historyArchive(t, map[string]historyArchiveState{"2026": {lifecycle: cache.SourceScopeActive, goals: 3, xgCovered: 19}})
	season := &archive[0]
	season.Data.Teams = []standings.Team{{ID: "alpha", Name: "Alpha"}, {ID: "bravo", Name: "Bravo"}}
	for index := range season.Data.Games {
		game := &season.Data.Games[index]
		game.RawJSON = `{"stadium_id":"7vQ7xbOMD1"}`
		game.KickoffUTC = time.Date(2026, 3, 1+index, 19, 0, 0, 0, time.UTC).Format(time.RFC3339)
		if index%2 == 0 {
			game.KickoffUTC = time.Date(2026, 3, 1+index, 19, 0, 0, 0, time.UTC).Format("2006-01-02 15:04:05 MST")
		}
		game.HomeScore.Int64, game.AwayScore.Int64 = int64(index%5), int64(index%3)
		if index == 1 {
			game.HomeTeamID, game.AwayTeamID = "bravo", "alpha"
		}
		if index < len(season.Data.XGoals) {
			xg := &season.Data.XGoals[index]
			xg.HomeTeamID, xg.AwayTeamID = game.HomeTeamID, game.AwayTeamID
			xg.HomeXG.Float64, xg.AwayXG.Float64 = float64(index)/10, .5
		}
	}
	// Deliberately reverse source order; an equivalent offset must sort by instant.
	season.Data.Games[0].KickoffUTC = "2026-03-02T00:00:00Z"
	season.Data.Games[1].KickoffUTC = "2026-03-01T23:30:00-08:00"
	season.Data.Games[1].HomeScore.Int64, season.Data.Games[1].AwayScore.Int64 = 3, 1
	slices.Reverse(season.Data.Games)
	summaries, err := history.SummarizeScoring(archive)
	if err != nil {
		t.Fatal(err)
	}
	teams, err := exploreTeams(nil, summaries, archive)
	if err != nil {
		t.Fatal(err)
	}
	for _, view := range []string{"balance", "compare", "difference"} {
		query := url.Values{"view": {"season-trend"}, "trend-view": {view}, "window": {"3"}}
		page, err := exploreSeasonTrend(query, teams, teamNameView{ID: "alpha"})
		if err != nil || len(page.TrendRows) != 20 || !page.TrendMissingXG || !page.TrendActive || page.TrendUndated || page.TrendVenueUnknown {
			t.Fatalf("%s: invalid trend: %+v, %v", view, page, err)
		}
		for index, row := range page.TrendRows {
			if row.ID != fmt.Sprintf("history-2026-%d", index) || row.Number != index+1 {
				t.Fatalf("source order or ID order replaced played order: %+v", row)
			}
		}
		first, away := page.TrendRows[0], page.TrendRows[1]
		if first.Opponent != "Bravo" || away.Venue != "Away" || away.Score != "1–3" || first.Date != "Mar 1, 2026" || away.Date != "Mar 1, 2026" || first.DisplayValues[0] != "Unavailable" || first.Values[0].Expected == nil || *first.Values[0].Expected != 0 {
			t.Fatalf("date/venue/zero-xG/full-window semantics: %+v / %+v", first, away)
		}
		if away.Values[0].Actual != 1 || away.Values[1].Actual != 3 || away.Values[2].Actual != -2 || *away.Values[0].Expected != .5 || *away.Values[1].Expected != .1 || *away.Values[2].Expected != .4 {
			t.Fatalf("orientation changed: %+v", away)
		}
		last := len(page.TrendColumns) - 1
		if page.TrendRows[18].DisplayValues[last] == "Unavailable" || page.TrendRows[19].DisplayValues[last] != "Unavailable" || page.TrendRows[19].DisplayValues[0] == "Unavailable" {
			t.Fatalf("%s: partial coverage changed: %+v", view, page.TrendRows)
		}
	}
}

func TestExploreSeasonTrendVenueDates(t *testing.T) {
	for _, test := range []struct {
		name, kickoff, stadium, id, date string
		undated, unknownVenue            bool
	}{
		{"ASA west coast", "2026-07-12 05:30:00 UTC", "7vQ7xbOMD1", "game", "Jul 11, 2026", false, false},
		{"same instant east coast", "2026-07-12T05:30:00Z", "vzqoJrj5ap", "game", "Jul 12, 2026", false, false},
		{"central", "2026-07-12T05:30:00Z", "xW5p3L0Mg1", "game", "Jul 12, 2026", false, false},
		{"mountain", "2026-07-12T05:30:00Z", "p6qb18650G", "game", "Jul 11, 2026", false, false},
		{"before spring DST", "2026-03-08T04:30:00Z", "vzqoJrj5ap", "game", "Mar 7, 2026", false, false},
		{"after spring DST", "2026-03-09T04:30:00Z", "vzqoJrj5ap", "game", "Mar 9, 2026", false, false},
		{"before fall DST", "2026-11-01T04:30:00Z", "vzqoJrj5ap", "game", "Nov 1, 2026", false, false},
		{"after fall DST", "2026-11-02T04:30:00Z", "vzqoJrj5ap", "game", "Nov 1, 2026", false, false},
		{"venue midnight", "2026-07-12T07:00:00Z", "7vQ7xbOMD1", "game", "Jul 12, 2026", false, false},
		{"unknown venue", "2026-07-12T05:30:00Z", "new-venue", "game", "Venue date unavailable", false, true},
		{"missing venue", "2026-07-12T05:30:00Z", "", "game", "Venue date unavailable", false, true},
		{"verified May correction", "2026-05-09 00:00:00 UTC", "", "Xj5YPveRMb", "May 8, 2026", false, false},
		{"verified August correction", "2026-08-07 23:00:00 UTC", "", "KXMeXv6vQ6", "Aug 7, 2026", false, false},
		{"corrected source wins", "2026-05-09T05:30:00Z", "7vQ7xbOMD1", "Xj5YPveRMb", "May 8, 2026", false, false},
		{"unknown source wins", "2026-05-09T05:30:00Z", "new-venue", "Xj5YPveRMb", "Venue date unavailable", false, true},
		{"invalid kickoff", "invalid", "7vQ7xbOMD1", "game", "Date unavailable", true, false},
		{"missing kickoff", "", "7vQ7xbOMD1", "game", "Date unavailable", true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Home/away orientation never changes which venue's calendar applies.
			for _, home := range []bool{true, false} {
				rows := exploreMatches([]history.TeamMatch{{ID: test.id, KickoffUTC: test.kickoff, StadiumID: test.stadium, Home: home}}, nil)
				if rows[0].Date != test.date || rows[0].Undated != test.undated || rows[0].VenueDateUnavailable != test.unknownVenue {
					t.Fatalf("home=%v: %+v", home, rows[0])
				}
			}
		})
	}
}

func TestExploreSeasonTrendUnknownVenuePreservesOrder(t *testing.T) {
	archive := historyArchive(t, map[string]historyArchiveState{"2026": {lifecycle: cache.SourceScopeActive, goals: 3}})
	for index := range archive[0].Data.Games {
		game := &archive[0].Data.Games[index]
		game.KickoffUTC = time.Date(2026, 3, index+1, 1, 0, 0, 0, time.UTC).Format("2006-01-02 15:04:05 MST")
		game.RawJSON = `{"stadium_id":"7vQ7xbOMD1"}`
	}
	archive[0].Data.Games[0].RawJSON = `{"stadium_id":"unknown"}`
	archive[0].Data.Games[1].RawJSON = `{invalid`
	archive[0].Data.Games[2].KickoffUTC = "invalid"
	slices.Reverse(archive[0].Data.Games)
	summaries, err := history.SummarizeScoring(archive)
	if err != nil {
		t.Fatal(err)
	}
	teams, err := exploreTeams(nil, summaries, archive)
	if err != nil {
		t.Fatal(err)
	}
	page, err := exploreSeasonTrend(nil, teams, teamNameView{ID: "alpha"})
	if err != nil || !page.TrendUndated || !page.TrendVenueUnknown || len(page.TrendRows) != 20 {
		t.Fatalf("missing date notices or dropped results: %+v, %v", page, err)
	}
	if page.TrendRows[0].ID != "history-2026-0" || page.TrendRows[1].ID != "history-2026-1" || page.TrendRows[19].ID != "history-2026-2" {
		t.Fatal("venue metadata must not change chronological ordering")
	}
}

func TestExploreSeasonTrendRollingWindows(t *testing.T) {
	matches := make([]exploreMatchRecord, 14)
	for index := range matches {
		for measure := range 3 {
			expected := float64(index-measure) + .1234
			matches[index].Values[measure] = exploreTeamValues{Actual: float64(index - measure), Expected: &expected}
		}
	}
	for _, window := range []int{3, 5, 10} {
		for measure := range 3 {
			for end := range matches {
				got := exploreRollingValue(matches, end, measure, window)
				if end+1 < window {
					if got.Expected != nil {
						t.Fatal("short prefix must not yield a rolling point")
					}
					continue
				}
				want := float64(end-measure) - float64(window-1)/2
				if math.Abs(got.Actual-want) > 1e-12 || got.Expected == nil || math.Abs(*got.Expected-(want+.1234)) > 1e-12 {
					t.Fatalf("window=%d measure=%d end=%d: %+v, want %f", window, measure, end, got, want)
				}
			}
		}
	}
	matches[3].Values[0].Expected = nil
	for end := 3; end < 6; end++ {
		if exploreRollingValue(matches, end, 0, 3).Expected != nil {
			t.Fatal("missing xG must withhold every window containing that match")
		}
	}
	if exploreRollingValue(matches, 6, 0, 3).Expected == nil {
		t.Fatal("xG rolling line must recover once the missing match leaves the window")
	}
}

func TestExploreSeasonTrendURLsFallbackAndEligibility(t *testing.T) {
	archive := historyArchive(t, map[string]historyArchiveState{
		"2024": {lifecycle: cache.SourceScopeCompleted, inventory: cache.InventoryCompletenessIncomplete, goals: 3},
		"2025": {lifecycle: cache.SourceScopeCompleted, goals: 3, xgCovered: 20},
		"2026": {lifecycle: cache.SourceScopeActive, goals: 2, xgCovered: 19},
	})
	for _, path := range []string{
		"/nwsl-season/explore?view=season-trend&season=2026&team=alpha&trend-view=balance&series=xg&trend-mode=rolling&window=10",
		"/explore?view=season-trend&season=2025&team=alpha&trend-view=difference&series=goals&trend-mode=match&window=3",
		"/explore?view=season-trend&season=2025&team=alpha&trend-view=compare&series=goals&trend-mode=match&window=3",
		"/explore?view=season-trend&season=2025&team=alpha&trend-view=relative&series=goals&trend-mode=match&window=3",
		"/explore?view=season-trend&season=2025&team=alpha&trend-view=relative&series=xg&trend-mode=rolling&window=10",
		"/explore?view=season-trend&season=2025&team=alpha&trend-view=relative&series=both&trend-mode=match&window=3",
		"/explore?view=season-trend&season=2025&team=alpha&trend-view=relative&series=both&average-series=xg&trend-mode=match&window=3",
		"/explore?view=season-trend&season=2025&team=alpha&trend-view=relative&series=xg&average-series=goals&trend-mode=match&window=3",
		"/explore?view=season-trend&season=2025&team=alpha&trend-view=relative&average-reference=league&average-series=xg",
		"/explore?view=season-trend&season=2024&team=alpha",
	} {
		store := &historyHTTPStore{archive: archive}
		response := httptest.NewRecorder()
		NewHandler(store).ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		body := response.Body.String()
		if response.Code != http.StatusOK || store.archiveCalls != 1 || store.seasonCalls != 0 {
			t.Fatalf("cache-only trend %s: %d %d/%d", path, response.Code, store.archiveCalls, store.seasonCalls)
		}
		for _, fragment := range []string{`data-view-choice="season-trend" aria-current="page"`, `data-panel="season-trend" aria-labelledby=`, `Show season trend`, `data-trend-rows`, `data-chart="season-trend"`} {
			if !strings.Contains(body, fragment) {
				t.Errorf("missing %s", fragment)
			}
		}
		if strings.Contains(path, "trend-view=relative") {
			for _, fragment := range []string{`value="relative" selected`, `data-trend-data-control hidden`, `data-trend-average-data-control>`, `Benchmarks</option>`, `References show each series`, `in the original units`} {
				if !strings.Contains(body, fragment) {
					t.Errorf("relative fallback missing %s", fragment)
				}
			}
			_, averagePicker, _ := strings.Cut(body, `<select name="average-series" data-trend-average-series>`)
			averagePicker, _, _ = strings.Cut(averagePicker, `</select>`)
			if strings.Contains(averagePicker, `value="both"`) || !strings.Contains(averagePicker, `value="goals"`) || !strings.Contains(averagePicker, `value="xg"`) {
				t.Fatal("Season averages must offer only Goals and xG")
			}
			_, referencePicker, _ := strings.Cut(body, `<select name="average-reference" form="season-trend-controls" data-trend-reference>`)
			referencePicker, _, _ = strings.Cut(referencePicker, `</select>`)
			reference := "league"
			if strings.Contains(path, "average-reference=league") {
				reference = "league"
			}
			if !strings.Contains(body, `data-trend-reference-control>`) || !strings.Contains(referencePicker, `value="`+reference+`" selected`) {
				t.Fatal("reference choice must be visible and restored in the native form")
			}
			parsed, err := url.ParseRequestURI(path)
			if err != nil {
				t.Fatal(err)
			}
			selected := parsed.Query().Get("average-series")
			if selected == "" {
				selected = parsed.Query().Get("series")
			}
			basis, other := "Goals", "xG"
			if selected == "xg" {
				basis, other = other, basis
			}
			if !strings.Contains(body, `<th scope="col">`+basis+` scored</th>`) || !strings.Contains(body, `<th scope="col">`+basis+` allowed</th>`) || strings.Contains(body, `<th scope="col">`+other+` scored</th>`) {
				t.Fatal("relative fallback must honor the Data selection")
			}
		} else if strings.Contains(path, "series=goals") {
			for _, fragment := range []string{`data-trend-data-control hidden`, `value="goals" selected`, `value="match" selected`, `value="3" selected`} {
				if !strings.Contains(body, fragment) {
					t.Errorf("comparison URL lost hidden Data preference or mode: %s", fragment)
				}
			}
			want := []string{"Goal differential", "xG differential"}
			if strings.Contains(path, "trend-view=compare") {
				want = []string{"Goals scored", "xG scored", "Goals allowed", "xG allowed"}
			}
			previous := -1
			for _, label := range want {
				index := strings.Index(body, `<th scope="col">`+label+`</th>`)
				if index <= previous {
					t.Fatalf("comparison fallback columns missing or out of order: %v", want)
				}
				previous = index
			}
		}
		if strings.Contains(path, "season=2024") {
			if !strings.Contains(body, `data-season-trend-results hidden`) {
				t.Fatal("known incomplete inventory must not produce a trend")
			}
		} else if strings.Contains(path, "season=2026") {
			for _, fragment := range []string{`value="10" selected`, `value="xg" selected`, `value="rolling" selected`, `10-match trailing averages, per match.`, `xG scored`, `xG allowed`, `data-chart="season-trend-xg"`, `data-trend-warning>`, `data-trend-undated>`} {
				if !strings.Contains(body, fragment) {
					t.Errorf("direct URL/fallback missing %s", fragment)
				}
			}
		}
	}
	for _, query := range []string{
		"window=", "window=4", "window=3&window=5", "trend-mode=", "trend-mode=total", "trend-mode=both", "trend-view=", "trend-view=for", "trend-view=balance&trend-view=difference", "trend-mode=match&trend-mode=rolling",
		"average-series=", "average-series=both", "average-series=goals&average-series=xg",
		"average-reference=", "average-reference=others", "average-reference=team&average-reference=league",
		"series=", "series=xg&series=goals", "team=", "team=unknown", "team=alpha&team=bravo", "season=2000",
	} {
		response := httptest.NewRecorder()
		NewHandler(&historyHTTPStore{archive: archive}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/explore?view=season-trend&"+query, nil))
		if response.Code != http.StatusBadRequest {
			t.Errorf("invalid %s returned %d", query, response.Code)
		}
	}
	response := httptest.NewRecorder()
	NewHandler(&historyHTTPStore{}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/explore?view=season-trend", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `data-season-trend-empty>No eligible`) {
		t.Fatal("empty archive should have an explicit empty state")
	}
}

func TestExploreSeasonTrendViewAndModeSelections(t *testing.T) {
	archive := historyArchive(t, map[string]historyArchiveState{"2026": {lifecycle: cache.SourceScopeActive, goals: 3, xgCovered: 19}})
	for index := range archive[0].Data.Games {
		archive[0].Data.Games[index].HomeScore.Int64 = int64(index % 4)
		archive[0].Data.Games[index].AwayScore.Int64 = int64(index % 3)
	}
	summaries, err := history.SummarizeScoring(archive)
	if err != nil {
		t.Fatal(err)
	}
	teams, err := exploreTeams(nil, summaries, archive)
	if err != nil {
		t.Fatal(err)
	}
	// A measure chosen in another team view must not override this view's default.
	teams.TeamMeasure = "points"
	defaults, err := exploreSeasonTrend(nil, teams, teamNameView{ID: "alpha"})
	if err != nil || defaults.TrendView != "balance" || defaults.TrendMode != "rolling" || defaults.TrendWindow != "5" || len(defaults.TrendColumns) != 4 {
		t.Fatalf("invalid scoring balance default: %+v, %v", defaults, err)
	}
	for _, view := range []string{"balance", "compare", "difference", "points", "relative"} {
		for _, series := range []string{"goals", "xg", "both"} {
			for _, mode := range []string{"match", "rolling"} {
				for _, window := range []int{3, 5, 10} {
					query := url.Values{"trend-view": {view}, "series": {series}, "trend-mode": {mode}, "window": {fmt.Sprint(window)}}
					page, err := exploreSeasonTrend(query, teams, teamNameView{ID: "alpha"})
					if err != nil {
						t.Fatal(err)
					}
					measures := []int{0, 1}
					if view == "difference" {
						measures = []int{2}
						if strings.Contains(page.TrendColumns[0].Label, "scored") {
							t.Fatal("differential must have its own value columns")
						}
					} else if view == "compare" {
						want := []exploreTrendColumn{{"Goals scored", 0, false}, {"xG scored", 0, true}, {"Goals allowed", 1, false}, {"xG allowed", 1, true}}
						if !slices.Equal(page.TrendColumns, want) {
							t.Fatalf("comparison must pair actual and xG per metric: %v", page.TrendColumns)
						}
					} else if view == "points" {
						measures = []int{3}
						if !slices.Equal(page.TrendColumns, []exploreTrendColumn{{"Points", 3, false}, {"xPoints", 3, true}}) {
							t.Fatalf("points columns = %v", page.TrendColumns)
						}
					} else if view == "relative" {
						if strings.Contains(page.TrendColumns[0].Label, "vs average") || page.TrendColumns[0].Measure != 0 || page.TrendColumns[len(page.TrendColumns)-1].Measure != 1 {
							t.Fatalf("relative columns must pair bases within scored/allowed: %v", page.TrendColumns)
						}
					} else if !strings.Contains(page.TrendColumns[0].Label, "scored") || !strings.Contains(page.TrendColumns[1].Label, "allowed") {
						t.Fatal("balance must show scored and allowed together")
					}
					wantColumns := len(measures)
					effectiveSeries := series
					if view == "compare" || view == "difference" || view == "points" {
						effectiveSeries = "both"
					}
					if view == "relative" && effectiveSeries == "both" {
						effectiveSeries = "goals"
					}
					if effectiveSeries == "both" {
						wantColumns *= 2
					}
					if len(page.TrendColumns) != wantColumns || page.TrendMissingXG != (view != "points" && effectiveSeries != "goals") {
						t.Fatalf("wrong series selection: %+v", page)
					}
					for end, row := range page.TrendRows {
						for columnIndex, column := range page.TrendColumns {
							start, count := end, 1
							if mode == "rolling" {
								start, count = end+1-window, window
							}
							want, total, covered := "Unavailable", 0.0, start >= 0
							if covered {
								for _, match := range page.TrendRows[start : end+1] {
									value := match.Values[column.Measure]
									if column.Expected {
										if value.Expected == nil {
											covered = false
											break
										}
										total += *value.Expected
									} else {
										total += value.Actual
									}
								}
							}
							if covered {
								average := total / float64(count)
								if math.Abs(average) < .005 {
									average = 0
								}
								want = fmt.Sprintf("%.2f", average)
							}
							if row.DisplayValues[columnIndex] != want {
								t.Fatalf("%v, match %d, column %s: got %s, want %s", query, end+1, column.Label, row.DisplayValues[columnIndex], want)
							}
						}
					}
				}
			}
		}
	}
}

func TestExploreSeasonTrendAverageViewPreservesValuesAndGaps(t *testing.T) {
	zero, one, three := 0.0, 1.0, 3.0
	matches := []exploreMatchRecord{
		{Values: [4]exploreTeamValues{{Actual: 0, Expected: &zero}, {Actual: 2, Expected: &one}}},
		{Values: [4]exploreTeamValues{{Actual: 4}, {Actual: 0}}},
		{Values: [4]exploreTeamValues{{Actual: 2, Expected: &three}, {Actual: 1, Expected: &three}}},
	}
	teams := exploreTeamsView{TeamSeason: "2026", TeamSeasons: []exploreTeamSeason{
		{Season: "2026", Teams: []exploreTeamRecord{{ID: "alpha", Matches: matches}}},
		{Season: "2025", Teams: []exploreTeamRecord{{ID: "alpha", Matches: []exploreMatchRecord{{Values: [4]exploreTeamValues{{Actual: 7, Expected: &zero}, {Actual: 4, Expected: &zero}}}}}}},
	}}
	for _, selection := range []struct{ series, reference string }{{"goals", "team"}, {"xg", "team"}, {"goals", "league"}, {"xg", "league"}} {
		series := selection.series
		teams.TeamSeason = "2026"
		query := url.Values{"trend-view": {"relative"}, "trend-mode": {"match"}, "series": {"both"}, "average-series": {series}, "average-reference": {selection.reference}, "window": {"3"}}
		page, err := exploreSeasonTrend(query, teams, teamNameView{ID: "alpha"})
		if err != nil {
			t.Fatal(err)
		}
		want := [][]string{{"0.00", "2.00"}, {"4.00", "0.00"}, {"2.00", "1.00"}}
		if series == "xg" {
			want = [][]string{{"0.00", "1.00"}, {"Unavailable", "Unavailable"}, {"3.00", "3.00"}}
		}
		for index, row := range page.TrendRows {
			if !slices.Equal(row.DisplayValues, want[index]) {
				t.Fatalf("%s, match %d: got %v, want %v", series, index+1, row.DisplayValues, want[index])
			}
		}
		query.Set("trend-mode", "rolling")
		page, err = exploreSeasonTrend(query, teams, teamNameView{ID: "alpha"})
		rolling := []string{"2.00", "1.00"}
		if series == "xg" {
			rolling = []string{"Unavailable", "Unavailable"}
		}
		if err != nil || !slices.Equal(page.TrendRows[2].DisplayValues, rolling) {
			t.Fatalf("average references must preserve rolling values and missing-xG windows: %+v, %v", page, err)
		}
		if zero != 0 || one != 1 || three != 3 || matches[0].Values[0].Actual != 0 {
			t.Fatal("average references must not mutate source observations")
		}
		teams.TeamSeason = "2025"
		query.Set("trend-mode", "match")
		page, err = exploreSeasonTrend(query, teams, teamNameView{ID: "alpha"})
		oneMatch := []string{"7.00", "4.00"}
		if series == "xg" {
			oneMatch = []string{"0.00", "0.00"}
		}
		if err != nil || !slices.Equal(page.TrendRows[0].DisplayValues, oneMatch) {
			t.Fatalf("one-match season must keep original values, including zero xG: %+v, %v", page, err)
		}
		query.Set("trend-mode", "rolling")
		page, err = exploreSeasonTrend(query, teams, teamNameView{ID: "alpha"})
		if err != nil || !slices.Equal(page.TrendRows[0].DisplayValues, []string{"Unavailable", "Unavailable"}) {
			t.Fatalf("average references must not invent a full rolling window: %+v, %v", page, err)
		}
	}
}
