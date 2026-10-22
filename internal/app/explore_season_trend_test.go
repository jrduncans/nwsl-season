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

	"github.com/jrduncans/nwsl-season/internal/apptest"
	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/history"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

func TestExploreSeasonTrendChronologyVenueAndPartialXG(t *testing.T) {
	archive := testArchive(t, map[string]archiveState{"2026": {Lifecycle: cache.SourceScopeActive, Goals: 3, XGCovered: 19}})
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
		if err != nil || len(page.TrendRows) != 20 || !page.TrendMissingXG || !page.TrendActive || page.TrendUndated || page.TrendVenueDateUnavailable {
			t.Fatalf("%s: invalid trend: %+v, %v", view, page, err)
		}
		for index, row := range page.TrendRows {
			if row.ID != fmt.Sprintf("history-2026-%d", index) || row.Number != index+1 {
				t.Fatalf("source order or ID order replaced played order: %+v", row)
			}
		}
		first, away := page.TrendRows[0], page.TrendRows[1]
		if first.Opponent != "Bravo" || away.Venue != "Away" || away.Score != "1–3" || first.Date != "Mar 1, 2026" || away.Date != "Mar 1, 2026" || first.DisplayValues[len(page.TrendTableColumns)-len(page.TrendColumns)] != exploreTrendNumber(&first.Values[page.TrendColumns[0].Measure].Actual) || first.Values[0].Expected == nil || *first.Values[0].Expected != 0 {
			t.Fatalf("date/venue/zero-xG/full-window semantics: %+v / %+v", first, away)
		}
		// The browser resolves the opponent logo from the season's team records.
		if first.OpponentID != "bravo" || !slices.ContainsFunc(teams.TeamSeasons[0].Teams, func(team exploreTeamRecord) bool {
			return team.ID == first.OpponentID && team.LogoURL == clubLogoURL("bravo")
		}) {
			t.Fatalf("opponent logo is not resolvable: %+v", first)
		}
		if away.Values[0].Actual != 1 || away.Values[1].Actual != 3 || away.Values[2].Actual != -2 || *away.Values[0].Expected != .5 || *away.Values[1].Expected != .1 || *away.Values[2].Expected != .4 {
			t.Fatalf("orientation changed: %+v", away)
		}
		last := len(page.TrendTableColumns) - 1
		if page.TrendRows[18].DisplayValues[last] == "Unavailable" || page.TrendRows[19].DisplayValues[last] != "Unavailable" || page.TrendRows[19].DisplayValues[0] == "Unavailable" {
			t.Fatalf("%s: partial coverage changed: %+v", view, page.TrendRows)
		}
	}
}

func TestExploreSeasonTrendVenueDates(t *testing.T) {
	for _, test := range []struct {
		name, kickoff, stadium, id, date string
		undated, venueDateUnavailable    bool
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
				venue := "Away"
				if home {
					venue = "Home"
				}
				if rows[0].Venue != venue {
					t.Fatalf("stadium metadata changed ASA designation: %+v", rows[0])
				}
				if rows[0].Date != test.date || rows[0].Undated != test.undated || rows[0].VenueDateUnavailable != test.venueDateUnavailable {
					t.Fatalf("home=%v: %+v", home, rows[0])
				}
			}
		})
	}
}

func TestExploreSeasonTrendUnknownVenuePreservesOrder(t *testing.T) {
	archive := testArchive(t, map[string]archiveState{"2026": {Lifecycle: cache.SourceScopeActive, Goals: 3}})
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
	if err != nil || !page.TrendUndated || !page.TrendVenueDateUnavailable || len(page.TrendRows) != 20 {
		t.Fatalf("missing date notices or dropped results: %+v, %v", page, err)
	}
	if page.TrendRows[0].ID != "history-2026-0" || page.TrendRows[1].ID != "history-2026-1" || page.TrendRows[19].ID != "history-2026-2" {
		t.Fatal("venue metadata must not change chronological ordering")
	}
}

func TestExploreSeasonTrendRollingWindows(t *testing.T) {
	matches := make([]exploreMatchRecord, 14)
	for index := range matches {
		for measure := range 4 {
			expected := float64(index-measure) + .1234
			matches[index].Values[measure] = exploreTeamValues{Actual: float64(index - measure), Expected: &expected}
		}
	}
	for _, window := range []int{3, 5, 10} {
		for measure := range 4 {
			for end := range matches {
				got := exploreRollingValue(matches, end, measure, window)
				want := float64(end-measure) - float64(min(end+1, window)-1)/2
				if math.Abs(got.Actual-want) > 1e-12 || got.Expected == nil || math.Abs(*got.Expected-(want+.1234)) > 1e-12 {
					t.Fatalf("window=%d measure=%d end=%d: %+v, want %f", window, measure, end, got, want)
				}
			}
		}
	}
	// A missing early observation must break even a shorter initial window,
	// while actual values keep averaging all played matches.
	for measure := range 4 {
		saved := matches[1].Values[measure].Expected
		matches[1].Values[measure].Expected = nil
		for _, window := range []int{3, 5, 10} {
			for end := range matches {
				got := exploreRollingValue(matches, end, measure, window)
				wantCovered := end == 0 || end > window
				wantActual := float64(end-measure) - float64(min(end+1, window)-1)/2
				if (got.Expected != nil) != wantCovered || math.Abs(got.Actual-wantActual) > 1e-12 {
					t.Fatalf("early missing observation: window=%d measure=%d end=%d: %+v", window, measure, end, got)
				}
			}
		}
		matches[1].Values[measure].Expected = saved
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
	archive := testArchive(t, map[string]archiveState{
		"2024": {Lifecycle: cache.SourceScopeCompleted, Inventory: cache.InventoryCompletenessIncomplete, Goals: 3},
		"2025": {Lifecycle: cache.SourceScopeCompleted, Goals: 3, XGCovered: 20},
		"2026": {Lifecycle: cache.SourceScopeActive, Goals: 2, XGCovered: 19},
	})
	for _, path := range []string{
		"/nwsl-season/explore?view=season-trend&season=2026&team=alpha&trend-view=balance&series=xg&smoothing=10",
		"/explore?view=season-trend&season=2025&team=alpha&trend-view=difference&series=goals&smoothing=none",
		"/explore?view=season-trend&season=2025&team=alpha&trend-view=compare&series=goals&smoothing=none",
		"/explore?view=season-trend&season=2025&team=alpha&trend-view=relative&series=goals&smoothing=none",
		"/explore?view=season-trend&season=2025&team=alpha&trend-view=relative&series=xg&smoothing=10",
		"/explore?view=season-trend&season=2025&team=alpha&trend-view=relative&series=both&smoothing=none",
		"/explore?view=season-trend&season=2025&team=alpha&trend-view=relative&series=both&average-series=xg&smoothing=none",
		"/explore?view=season-trend&season=2025&team=alpha&trend-view=relative&series=xg&average-series=goals&smoothing=none",
		"/explore?view=season-trend&season=2025&team=alpha&trend-view=relative&average-reference=league&average-series=xg",
		"/explore?view=season-trend&season=2024&team=alpha",
	} {
		store := &archiveHTTPStore{archive: archive}
		response := httptest.NewRecorder()
		NewHandler(store).ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		body := response.Body.String()
		if response.Code != http.StatusOK || store.archiveCalls != 1 || store.seasonCalls != 0 {
			t.Fatalf("cache-only trend %s: %d %d/%d", path, response.Code, store.archiveCalls, store.seasonCalls)
		}
		for _, fragment := range []string{`data-view-choice="season-trend" aria-current="page"`, `data-panel="season-trend" aria-labelledby=`, `data-trend-chart-card`, `data-trend-rows`, `data-chart="season-trend"`} {
			if !strings.Contains(body, fragment) {
				t.Errorf("missing %s", fragment)
			}
		}
		if strings.Contains(path, "trend-view=relative") {
			for _, fragment := range []string{`value="relative" form="season-trend-controls" checked`, `data-trend-average-data-control>`, `<span>Benchmarks</span>`, `Reference lines show the team’s season average`} {
				if !strings.Contains(body, fragment) {
					t.Errorf("relative fallback missing %s", fragment)
				}
			}
			_, averagePicker, _ := strings.Cut(body, `data-trend-average-series data-trend-average-data-control>`)
			averagePicker, _, _ = strings.Cut(averagePicker, `</fieldset>`)
			if strings.Contains(averagePicker, `value="both"`) || !strings.Contains(averagePicker, `value="goals"`) || !strings.Contains(averagePicker, `value="xg"`) {
				t.Fatal("Season averages must offer only Goals and xG")
			}
			_, referencePicker, _ := strings.Cut(body, `<select name="average-reference" form="season-trend-controls" data-trend-reference aria-label="Benchmark">`)
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
			basis, other := "Goals", "xG"
			if selected == "xg" {
				basis, other = other, basis
			}
			requireText(t, body, "[data-trend-headings]", "Score", "Points", "xG for", "xG against", "xPoints")
			if parsed.Query().Get("smoothing") != "none" {
				window := parsed.Query().Get("smoothing")
				if window == "" {
					window = "5"
				}
				if !strings.Contains(body, basis+" scored · "+window+"-match average") || strings.Contains(body, other+" scored · "+window+"-match average") {
					t.Fatal("relative rolling columns must honor the Data selection")
				}
			}
		} else if strings.Contains(path, "series=goals") {
			for _, fragment := range []string{`data-trend-carried-series value="goals"`, `value="none" form="season-trend-controls" checked`} {
				if !strings.Contains(body, fragment) {
					t.Errorf("comparison URL lost hidden Data preference or mode: %s", fragment)
				}
			}
			want := []string{"Goal margin", "xG margin"}
			if strings.Contains(path, "trend-view=compare") {
				want = []string{"Goals − xG", "Goals allowed − xG allowed"}
			}
			previous := -1
			for _, label := range want {
				index := strings.Index(body, `aria-label="Sort by `+label+`">`+label+`</a>`)
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
			for _, fragment := range []string{`value="10" form="season-trend-controls" checked`, `data-trend-carried-series value="xg"`, `10-match trailing averages, per match; matches 1–9 use all matches played so far.`, `xG scored`, `xG allowed`, `data-chart="season-trend-xg"`, `data-trend-warning>`, `data-trend-undated>`} {
				if !strings.Contains(body, fragment) {
					t.Errorf("direct URL/fallback missing %s", fragment)
				}
			}
		}
	}
	for _, query := range []string{
		"window=", "window=4", "window=3&window=5", "trend-mode=", "trend-mode=total", "trend-mode=both", "trend-view=", "trend-view=for", "trend-view=balance&trend-view=difference", "smoothing=none&smoothing=5", "smoothing=", "smoothing=7", "smoothing=rolling",
		"average-series=", "average-series=both", "average-series=goals&average-series=xg",
		"average-reference=", "average-reference=others", "average-reference=team&average-reference=league",
		"team=", "team=unknown", "team=alpha&team=bravo", "season=2000",
	} {
		response := httptest.NewRecorder()
		NewHandler(&archiveHTTPStore{archive: archive}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/explore?view=season-trend&"+query, nil))
		if response.Code != http.StatusBadRequest {
			t.Errorf("invalid %s returned %d", query, response.Code)
		}
	}
	for _, query := range []string{"series=", "series=invalid", "series=xg&series=goals"} {
		response := httptest.NewRecorder()
		NewHandler(&archiveHTTPStore{archive: archive}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/explore?view=season-trend&"+query, nil))
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `data-trend-carried-series value="both"`) {
			t.Errorf("malformed carried %s should be ignored and default to both: status %d", query, response.Code)
		}
	}
	response := httptest.NewRecorder()
	NewHandler(&archiveHTTPStore{}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/explore?view=season-trend", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `data-season-trend-empty>No eligible`) {
		t.Fatal("empty archive should have an explicit empty state")
	}
}

func TestExploreSeasonTrendViewAndModeSelections(t *testing.T) {
	archive := testArchive(t, map[string]archiveState{"2026": {Lifecycle: cache.SourceScopeActive, Goals: 3, XGCovered: 19}})
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
						want := []exploreTrendColumn{{Label: "Goals scored", Measure: 0}, {Label: "xG scored", Measure: 0, Expected: true}, {Label: "Goals allowed", Measure: 1}, {Label: "xG allowed", Measure: 1, Expected: true}}
						if !slices.Equal(page.TrendColumns[:4], want) {
							t.Fatalf("comparison must pair actual and xG per metric: %v", page.TrendColumns)
						}
					} else if view == "points" {
						measures = []int{3}
						if !slices.Equal(page.TrendColumns[:2], []exploreTrendColumn{{Label: "Points", Measure: 3}, {Label: "xPoints", Measure: 3, Expected: true}}) {
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
					effectiveSeries := "both"
					if view == "relative" {
						effectiveSeries = "goals"
					}
					if effectiveSeries == "both" {
						wantColumns *= 2
					}
					if len(page.TrendColumns) != wantColumns || page.TrendMissingXG != (view != "points" && effectiveSeries != "goals") {
						t.Fatalf("wrong series selection: %+v", page)
					}
					for end, row := range page.TrendRows {
						for columnIndex, column := range page.TrendTableColumns {
							if column.Gap || column.Field != "" {
								continue
							}
							start, count := end, 1
							if column.Rolling {
								count = min(end+1, window)
								start = end + 1 - count
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
								if !column.Rolling && !column.Expected {
									want = fmt.Sprintf("%.0f", average)
								}
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
		{GoalsCovered: true, Values: [4]exploreTeamValues{{Actual: 0, Expected: &zero}, {Actual: 2, Expected: &one}}},
		{GoalsCovered: true, Values: [4]exploreTeamValues{{Actual: 4}, {Actual: 0}}},
		{GoalsCovered: true, Values: [4]exploreTeamValues{{Actual: 2, Expected: &three}, {Actual: 1, Expected: &three}}},
	}
	teams := exploreTeamsView{TeamSeason: "2026", TeamSeasons: []exploreTeamSeason{
		{Season: "2026", Teams: []exploreTeamRecord{{ID: "alpha", Matches: matches}}},
		{Season: "2025", Teams: []exploreTeamRecord{{ID: "alpha", Matches: []exploreMatchRecord{{GoalsCovered: true, Values: [4]exploreTeamValues{{Actual: 7, Expected: &zero}, {Actual: 4, Expected: &zero}}}}}}},
	}}
	for _, selection := range []struct{ series, reference string }{{"goals", "team"}, {"xg", "team"}, {"goals", "league"}, {"xg", "league"}} {
		series := selection.series
		teams.TeamSeason = "2026"
		query := url.Values{"trend-view": {"relative"}, "trend-mode": {"match"}, "series": {"both"}, "average-series": {series}, "average-reference": {selection.reference}, "window": {"3"}}
		page, err := exploreSeasonTrend(query, teams, teamNameView{ID: "alpha"})
		if err != nil {
			t.Fatal(err)
		}
		want := [][]string{{"0.00", "1.00", "Unavailable"}, {"Unavailable", "Unavailable", "Unavailable"}, {"3.00", "3.00", "Unavailable"}}
		for index, row := range page.TrendRows {
			if !slices.Equal(row.DisplayValues[7:], want[index]) {
				t.Fatalf("%s, match %d: got %v, want %v", series, index+1, row.DisplayValues, want[index])
			}
		}
		query.Set("trend-mode", "rolling")
		page, err = exploreSeasonTrend(query, teams, teamNameView{ID: "alpha"})
		rolling := []string{"2.00", "1.00"}
		if series == "xg" {
			rolling = []string{"Unavailable", "Unavailable"}
		}
		if err != nil || !slices.Equal(page.TrendRows[2].DisplayValues[10:], rolling) {
			t.Fatalf("average references must preserve rolling values and missing-xG windows: %+v, %v", page, err)
		}
		if zero != 0 || one != 1 || three != 3 || matches[0].Values[0].Actual != 0 {
			t.Fatal("average references must not mutate source observations")
		}
		teams.TeamSeason = "2025"
		query.Set("trend-mode", "match")
		page, err = exploreSeasonTrend(query, teams, teamNameView{ID: "alpha"})
		oneMatch := []string{"0.00", "0.00", "Unavailable"}
		if err != nil || !slices.Equal(page.TrendRows[0].DisplayValues[7:], oneMatch) {
			t.Fatalf("one-match season must keep original values, including zero xG: %+v, %v", page, err)
		}
		query.Set("trend-mode", "rolling")
		oneMatch = []string{"7.00", "4.00"}
		if series == "xg" {
			oneMatch = []string{"0.00", "0.00"}
		}
		page, err = exploreSeasonTrend(query, teams, teamNameView{ID: "alpha"})
		if err != nil || !slices.Equal(page.TrendRows[0].DisplayValues[10:], oneMatch) {
			t.Fatalf("one-match rolling averages must preserve the original value: %+v, %v", page, err)
		}
	}
}

// Old trend-mode and window links redirect to the single Smoothing value,
// keeping every other selection; an explicit smoothing value wins.
func TestExploreSeasonTrendLegacySmoothingRedirects(t *testing.T) {
	t.Parallel()
	archive := testArchive(t, map[string]archiveState{"2025": {Lifecycle: cache.SourceScopeCompleted, Goals: 3, XGCovered: 20}})
	for legacy, want := range map[string]string{
		"trend-mode=match":                  "none",
		"trend-mode=match&window=10":        "none",
		"trend-mode=rolling":                "5",
		"window=3":                          "3",
		"trend-mode=rolling&window=10":      "10",
		"trend-mode=rolling&smoothing=none": "none",
	} {
		response := httptest.NewRecorder()
		NewHandler(&archiveHTTPStore{archive: archive}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/nwsl-season/explore?view=season-trend&season=2025&team=alpha&trend-view=points&"+legacy, nil))
		if response.Code != http.StatusFound {
			t.Fatalf("%s: status %d", legacy, response.Code)
		}
		target, err := url.Parse(response.Header().Get("Location"))
		if err != nil || target.Path != "" || target.Query().Has("trend-mode") || target.Query().Has("window") || target.Query().Get("smoothing") != want || target.Query().Get("trend-view") != "points" || target.Query().Get("team") != "alpha" {
			t.Fatalf("%s: redirect %s, %v", legacy, target, err)
		}
	}
	code, body := getPage(t, archive, "/explore?view=season-trend&season=2025&team=alpha&smoothing=10")
	if code != http.StatusOK {
		t.Fatal(code)
	}
	requireAttr(t, body, "[data-trend-smoothing] input[value='10']", "checked", "")
	forbidElements(t, body, "select[name=trend-mode], select[name=window], [data-trend-window-control]")
}

// The chart card's segmented controls and switches render the URL's selection,
// and the status line names each active switch.
func TestExploreSeasonTrendChartCard(t *testing.T) {
	archive := apptest.Scenario(t, apptest.ScenarioSeasonTrend)
	path := "/explore?view=season-trend&season=2025&team=" + opponentsTeam
	_, plain := getPage(t, archive, path)
	requireExact(t, plain, "[data-trend-view] legend", "Chart")
	requireExact(t, plain, "[data-trend-view] label span", "Scoring balance", "Goals vs xG", "Margins", "Points vs xPoints", "Benchmarks")
	requireAttr(t, plain, "[data-trend-view] input[value=balance]", "checked", "")
	requireExact(t, plain, "[data-trend-smoothing] legend", "Smoothing")
	requireExact(t, plain, "[data-trend-smoothing] label span", "None", "3", "5", "10")
	requireAttr(t, plain, "[data-trend-smoothing] input[value='5']", "checked", "")
	requireAttr(t, plain, "[data-trend-smoothing] input[value='5']", "aria-label", "5-match average")
	requireAttr(t, plain, "[data-trend-chart-card] input[data-penalties-selector]", "form", "season-trend-controls")
	requireAttr(t, plain, "[data-trend-chart-card] input[data-penalties-selector]", "role", "switch")
	requireAttr(t, plain, "[data-trend-opponents]", "role", "switch")
	requireElements(t, plain, `[data-trend-basis="non-penalty"][hidden]`, `[data-trend-basis="opponents"][hidden]`)
	forbidElements(t, plain, "[data-trend-submit]", "[data-season-trend-controls] noscript")

	_, adjusted := getPage(t, archive, path+"&trend-view=difference&smoothing=none&penalties=exclude&opponents=adjusted")
	requireAttr(t, adjusted, "[data-trend-view] input[value=difference]", "checked", "")
	requireAttr(t, adjusted, "[data-trend-smoothing] input[value=none]", "checked", "")
	forbidElements(t, adjusted, `[data-trend-basis][hidden]`)
	requireText(t, adjusted, "[data-trend-season-status]", "· non-penalty · adjusted for opponents")
	requireElements(t, adjusted, "[data-trend-average-data-control][hidden]", "[data-trend-reference-control][hidden]")

	// Benchmarks adds Show beside Smoothing and picks the benchmark in the
	// legend, keyed in the selected series' color.
	_, relative := getPage(t, archive, path+"&trend-view=relative&average-series=xpoints")
	forbidElements(t, relative, "[data-trend-average-data-control][hidden]", "[data-trend-reference-control][hidden]")
	requireExact(t, relative, "[data-trend-average-series] legend", "Show")
	requireExact(t, relative, "[data-trend-average-series] label span", "Goals", "xG", "Points", "xPoints")
	requireAttr(t, relative, "[data-trend-average-series] input[value=xpoints]", "checked", "")
	requireText(t, relative, "[data-trend-reference-control]", "Team average", "Compared with")
	requireAttr(t, relative, "[data-trend-reference-key]", "data-series", "xg")
	requireElements(t, relative, "[data-season-trend-plot] [data-trend-reference-control] select[data-trend-reference]")
}
