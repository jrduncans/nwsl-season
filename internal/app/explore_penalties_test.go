package app

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/history"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

func penaltyArchive(t *testing.T, splits int) []cache.HistoricalSeason {
	t.Helper()
	return historyArchive(t, map[string]historyArchiveState{
		"2025": {Lifecycle: cache.SourceScopeCompleted, Goals: 3, XGCovered: 20, PenaltySplits: splits},
	})
}

func getPage(t *testing.T, archive []cache.HistoricalSeason, target string) (int, string) {
	t.Helper()
	response := httptest.NewRecorder()
	NewHandler(&historyHTTPStore{archive: archive}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
	return response.Code, response.Body.String()
}

func TestPenaltiesParameterIsRejectedLikeAnInvalidMeasure(t *testing.T) {
	archive := penaltyArchive(t, 20)
	for _, target := range []string{
		"/explore?view=teams&penalties=bogus", "/explore?penalties=", "/explore?penalties=include&penalties=exclude",
		"/explore?view=trend&penalties=Exclude", "/history/scoring?penalties=bogus", "/history/scoring?penalties=exclude&penalties=exclude",
	} {
		code, body := getPage(t, archive, target)
		if code != http.StatusBadRequest {
			t.Errorf("%s status = %d, want 400", target, code)
		}
		requireText(t, body, "main", "penalties must be include or exclude")
	}
	code, body := getPage(t, archive, "/explore?view=teams&measure=bogus")
	if code != http.StatusBadRequest {
		t.Fatalf("invalid measure status = %d", code)
	}
	requireExact(t, body, "main h1", "Invalid history selection")
	_, penalties := getPage(t, archive, "/explore?penalties=bogus")
	requireExact(t, penalties, "main h1", "Invalid history selection")
}

func TestPenaltiesDefaultBasisKeepsLinksAndOnlyAddsTheControl(t *testing.T) {
	archive := penaltyArchive(t, 20)
	_, plain := getPage(t, archive, "/explore?view=team-history&team=alpha&season=2025")
	_, included := getPage(t, archive, "/explore?view=team-history&team=alpha&season=2025&penalties=include")
	// The explicit default differs only in the echoed selection state.
	requireAttr(t, plain, `select[data-penalties-selector] option[selected]`, "value", "include")
	requireAttr(t, included, `select[data-penalties-selector] option[selected]`, "value", "include")
	if plain != included {
		t.Error("penalties=include must render exactly as an absent parameter")
	}
	forbidRaw(t, plain, "npxG", "Non-penalty", `name="penalties"`, `data-points-note`, `data-penalties="exclude"`)
	// Only the Excluded destination names the parameter; default links omit it.
	// Every view carries its own select and destination links.
	if got, want := len(find(t, plain, `a[data-penalties-choice=exclude]`)), len(find(t, plain, `label.penalties-select`)); got != want || want != 7 {
		t.Errorf("default page has %d Excluded destinations for %d selects, want 7 each", got, want)
	}
	for _, link := range find(t, plain, `a[href*=penalties]`) {
		if attr(link, "data-penalties-choice") != "exclude" && attr(link, "class") != "button" {
			t.Errorf("default page link %q names penalties", attr(link, "href"))
		}
	}
	requireAttr(t, plain, `a[data-penalties-choice=exclude]`, "href", "?penalties=exclude&season=2025&team=alpha&view=team-history")
	requireAttr(t, plain, `a[data-penalties-choice=include]`, "href", "?season=2025&team=alpha&view=team-history")
	// The control is a labeled select in the data-control row, never in navigation.
	requireText(t, plain, "form[data-team-history-controls] label.penalties-select", "Penalties", "Included", "Excluded")
	requireAttr(t, plain, `select[data-penalties-selector]`, "title", "Excluded removes penalty kicks from both goals and xG.")
	forbidRaw(t, plain, "explore-penalties", "data-penalties-control", "Penalty kicks excluded.")
	if got := len(find(t, plain, `nav a[data-penalties-choice], .explore header ~ a[data-penalties-choice]`)); got != 0 {
		t.Errorf("%d penalties links in navigation", got)
	}
}

func TestExplorePenaltiesExcludedComputesAndLabelsNonPenaltyBasis(t *testing.T) {
	archive := penaltyArchive(t, 20)
	all, err := history.SummarizeScoring(archive)
	if err != nil {
		t.Fatal(err)
	}
	np, err := history.SummarizeScoringBasis(archive, history.NonPenalty)
	if err != nil {
		t.Fatal(err)
	}
	basic, err := exploreTeams(nil, all, archive)
	if err != nil {
		t.Fatal(err)
	}
	page, err := exploreTeams(url.Values{"penalties": {"exclude"}}, np, archive)
	if err != nil {
		t.Fatal(err)
	}
	var alpha, official exploreTeamRecord
	for _, team := range page.TeamSeasons[0].Teams {
		if team.ID == "alpha" {
			alpha = team
		}
	}
	for _, team := range basic.TeamSeasons[0].Teams {
		if team.ID == "alpha" {
			official = team
		}
	}
	// Alpha hosts every game: 1 goal (a penalty) and 1.00 xG (0.76 penalty).
	if official.Totals[0].Actual != 20 || *official.Totals[0].Expected != 20 {
		t.Fatalf("default totals = %+v", official.Totals[0])
	}
	if alpha.Totals[0].Actual != 0 || alpha.Totals[1].Actual != 40 || alpha.Totals[2].Actual != -40 {
		t.Fatalf("non-penalty goal totals = %+v", alpha.Totals)
	}
	if got := *alpha.Totals[0].Expected; got < 4.79 || got > 4.81 {
		t.Fatalf("non-penalty xG for = %v, want 20 x 0.24", got)
	}
	if alpha.Totals[3] != official.Totals[3] || alpha.Values[3] != official.Values[3] {
		t.Fatalf("points and xPts changed with the basis: %+v vs %+v", alpha.Totals[3], official.Totals[3])
	}
	if page.TeamActualLabel != "Non-penalty goal differential" || page.TeamXGLabel != "npxG differential" {
		t.Fatalf("labels = %q / %q", page.TeamActualLabel, page.TeamXGLabel)
	}
	if !strings.Contains(page.TeamChartURL, "penalties=exclude") || !strings.Contains(page.TeamTableURL, "penalties=exclude") {
		t.Fatalf("selection links lost the basis: %s %s", page.TeamChartURL, page.TeamTableURL)
	}
	for _, column := range page.TeamColumns {
		if !strings.Contains(column.URL, "penalties=exclude") {
			t.Errorf("sort link %q lost the basis: %s", column.Key, column.URL)
		}
	}
	for _, rank := range alpha.Rankings[0] {
		if strings.Contains(rank.Label, "Goals") || strings.Contains(rank.Label, "xG ") && !strings.Contains(rank.Label, "npxG") {
			t.Errorf("ranking label %q was not switched", rank.Label)
		}
	}
}

func TestExplorePenaltiesExcludedRendersEveryViewWithTheBasisAndNote(t *testing.T) {
	archive := penaltyArchive(t, 20)
	for _, view := range []string{
		"view=teams&display=chart", "view=teams&display=gap", "view=teams&display=scatter", "view=teams&display=quadrant", "view=teams&display=table",
		"view=team-rankings", "view=season-trend", "view=team-history", "view=trend", "view=distribution", "view=table",
	} {
		t.Run(view, func(t *testing.T) {
			code, body := getPage(t, archive, "/explore?"+view+"&team=alpha&penalties=exclude")
			if code != http.StatusOK {
				t.Fatalf("status = %d", code)
			}
			requireElements(t, body, `[data-explore][data-penalties=exclude]`)
			requireAttr(t, body, `select[data-penalties-selector] option[selected]`, "value", "exclude")
			panel := strings.TrimPrefix(strings.SplitN(view, "&", 2)[0], "view=")
			if got := len(find(t, body, `section[data-panel=`+panel+`] label.penalties-select`)); got != 1 {
				t.Errorf("the %s view offers %d Penalties selects, want 1", panel, got)
			}
			if panel != "trend" && panel != "table" {
				requireText(t, body, "section[data-panel="+panel+"]", "Penalty kicks excluded.")
			}
			if got := len(find(t, body, `nav label.penalties-select, nav select`)); got != 0 {
				t.Errorf("the Penalties select is inside navigation")
			}
			// Every state-preserving link and form carries the basis.
			for _, link := range find(t, body, `nav.explore-views a, nav.explore-subviews a`) {
				if !strings.Contains(attr(link, "href"), "penalties=exclude") {
					t.Errorf("link %q lost penalties=exclude: %s", attr(link, "href"), view)
				}
			}
			forms := find(t, body, `form.explore-team-controls`)
			inputs := find(t, body, `form.explore-team-controls input[name=penalties][value=exclude]`)
			// Every selection form that is rendered carries the basis.
			if len(forms) == 0 || len(inputs) != len(forms) {
				t.Errorf("%d selection forms, %d with the hidden penalties input", len(forms), len(inputs))
			}
		})
	}

	// Labels switch on the server-rendered markup.
	_, table := getPage(t, archive, "/explore?view=table&penalties=exclude")
	requireText(t, table, "[data-panel=table] thead", "Non-penalty goals/match", "npxG/match", "Non-penalty goals − npxG/match")
	requireRowText(t, table, "[data-panel=table] tbody tr", "2025", "20", "2.00", "0.24", "1.76")
	_, trend := getPage(t, archive, "/explore?view=trend&penalties=exclude")
	requireText(t, trend, "[data-panel=trend]", "Non-penalty goals and chances per match", "Non-penalty expected goals (npxG)", "Above npxG")
	// Data-control option labels never change with the basis; the adjacent
	// Penalties select states it.
	for _, view := range []string{"trend", "distribution", "teams", "team-history", "season-trend", "team-rankings", "table"} {
		_, body := getPage(t, archive, "/explore?view="+view+"&penalties=exclude")
		for _, option := range find(t, body, `select option`) {
			if label := strings.TrimSpace(text(option)); strings.Contains(label, "on-penalty") || strings.Contains(label, "npxG") {
				t.Errorf("%s: option label %q changed with the basis", view, label)
			}
		}
	}
	_, distribution := getPage(t, archive, "/explore?view=distribution&penalties=exclude")
	requireText(t, distribution, "[data-panel=distribution]", "by total non-penalty goals", "2 non-penalty goals", "Bins count non-penalty goals per match")
	requireText(t, distribution, "[data-panel=distribution] table thead", "2 non-penalty goals")
	_, defaults := getPage(t, archive, "/explore?view=table")
	requireRowText(t, defaults, "[data-panel=table] tbody tr", "2025", "20", "3.00", "1.00", "2.00")

	// The points note appears wherever points or xPts are shown, only on the
	// non-penalty basis, and never changes the values.
	shown := func(target string) bool {
		_, body := getPage(t, archive, target)
		notes := find(t, body, `[data-points-note]`)
		for _, note := range notes {
			if _, hidden := lookupAttr(note, "hidden"); !hidden {
				return true
			}
		}
		return false
	}
	for target, want := range map[string]bool{
		"/explore?view=teams&display=chart&measure=points&penalties=exclude":                      true,
		"/explore?view=teams&display=table&penalties=exclude":                                     true,
		"/explore?view=teams&display=chart&measure=for&penalties=exclude":                         false,
		"/explore?view=teams&display=quadrant&measure=points&penalties=exclude":                   false,
		"/explore?view=team-history&penalties=exclude":                                            true,
		"/explore?view=season-trend&trend-view=points&penalties=exclude":                          true,
		"/explore?view=season-trend&trend-view=relative&average-series=xpoints&penalties=exclude": true,
		"/explore?view=season-trend&trend-view=compare&penalties=exclude":                         false,
		"/explore?view=teams&display=chart&measure=points":                                        false,
		"/explore?view=team-history":                                                              false,
	} {
		if got := shown(target); got != want {
			t.Errorf("%s: points note shown = %v, want %v", target, got, want)
		}
	}
	_, body := getPage(t, archive, "/explore?view=team-history&penalties=exclude")
	requireText(t, body, "[data-points-note=team-history]", pointsBasisNote)
}

func TestExplorePenaltiesIncompleteSeasonIsExcludedWithTheNonPenaltyReason(t *testing.T) {
	archive := penaltyArchive(t, 19)
	code, body := getPage(t, archive, "/explore?view=table&penalties=exclude")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	requireText(t, body, "[data-panel=table]", "No seasons currently have enough complete results to compare.")
	_, histPage := getPage(t, archive, "/history/scoring?penalties=exclude")
	requireText(t, histPage, "main", "2025 — incomplete non-penalty data")
	_, teams := getPage(t, archive, "/explore?view=teams&penalties=exclude")
	requireElements(t, teams, `[data-team-empty]`)
	forbidRaw(t, teams, `"alpha"`)
	// The default basis is unaffected by the missing split.
	_, defaults := getPage(t, archive, "/explore?view=table")
	requireRowText(t, defaults, "[data-panel=table] tbody tr", "2025", "20", "3.00", "1.00")
}

func TestHistoryPagePenaltiesControlLabelsAndLinks(t *testing.T) {
	archive := penaltyArchive(t, 20)
	_, plain := getPage(t, archive, "/history/scoring?season=2025&metric=xg")
	requireAttr(t, plain, `a[data-penalties-choice=exclude]`, "href", "scoring?metric=xg&penalties=exclude&season=2025")
	requireText(t, plain, "form.history-selector", "Season", "Penalties", "Included", "Excluded")
	forbidRaw(t, plain, "data-penalties-control", "history-penalties")
	requireAttr(t, plain, `a[data-penalties-choice=include]`, "href", "scoring?metric=xg&season=2025")
	forbidRaw(t, plain, `name="penalties"`, "Non-penalty")
	if got := len(find(t, plain, `a[data-penalties-choice=exclude]`)); got != 1 {
		t.Errorf("default history page has %d Excluded destinations, want 1", got)
	}

	code, body := getPage(t, archive, "/history/scoring?season=2025&metric=compare&penalties=exclude")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	requireText(t, body, "main", "Penalties", "Included", "Excluded", "Excluded removes penalty kicks from both goals and xG.")
	requireAttr(t, body, `select[data-penalties-selector] option[selected]`, "value", "exclude")
	requireElements(t, body, `form.history-selector input[name=penalties][value=exclude]`, `form.history-selector input[name=metric][value=compare]`)
	requireText(t, body, ".history-metric-choice", "Non-penalty goals", "Non-penalty expected goals (npxG)")
	requireText(t, body, ".history-primary-metrics", "Non-penalty goals per match", "npxG per match", "2.00", "0.24")
	requireText(t, body, ".history-difference", "Non-penalty goals minus npxG per match", "1.76")
	requireText(t, body, "section.history-distribution", "Share of matches by total non-penalty goals", "2 non-penalty goals")
	for _, link := range find(t, body, `a.history-metric-link, a.history-chart-point-link, .history-distribution-row a`) {
		if href := attr(link, "href"); !strings.Contains(href, "penalties=exclude") && attr(link, "data-penalties-choice") != "include" {
			t.Errorf("history link %q lost the basis", href)
		}
	}
	requireText(t, body, "table.history-table thead", "Total non-penalty goals", "Non-penalty goals per match", "npxG per match")
}

// npxgSeasonData has two completed alpha-bravo games with xG. Alpha has 3.00
// xG for across them, 0.76 of it from a first-game penalty.
func npxgSeasonData(firstSplit, secondSplit *cache.PenaltySplit) cache.SeasonData {
	data := testSeasonData()
	data.Games = append(data.Games, cache.Game{
		ASAID: "completed-2", Season: "2026", Stage: "Regular Season", KickoffUTC: "2026-07-05 19:00:00 UTC",
		Status: standings.CompletedStatus, HomeTeamID: "bravo", AwayTeamID: "alpha",
		HomeScore: sql.NullInt64{Int64: 0, Valid: true}, AwayScore: sql.NullInt64{Int64: 1, Valid: true}, Matchday: sql.NullInt64{Int64: 2, Valid: true},
	})
	xg := func(id, home, away string, homeXG, awayXG float64, split *cache.PenaltySplit) cache.GameXG {
		return cache.GameXG{
			GameID: id, Availability: cache.XGAvailable, HomeTeamID: home, AwayTeamID: away,
			HomeXG: sql.NullFloat64{Float64: homeXG, Valid: true}, AwayXG: sql.NullFloat64{Float64: awayXG, Valid: true},
			Penalties: split,
		}
	}
	// Alpha: 2.36 + 0.64 = 3.00 xG for, 1.11 + 1.00 = 2.11 against.
	data.XGoals = []cache.GameXG{
		xg("completed", "alpha", "bravo", 2.36, 1.11, firstSplit),
		xg("completed-2", "bravo", "alpha", 1.00, 0.64, secondSplit),
	}
	return data
}

// Standings stay on official results and team-model xG: a season-total npxG
// difference cannot rank a table the way per-game points do, so non-penalty
// xG lives in Explore and the fixture results instead.
func TestStandingsDoNotOfferNonPenaltyXG(t *testing.T) {
	response := httptest.NewRecorder()
	data := npxgSeasonData(&cache.PenaltySplit{HomeXG: .76, HomeGoals: 1, HomeShots: 1}, &cache.PenaltySplit{})
	NewHandlerWithOptions(fakeStore{season: data}, Options{Rules: testRules(30), Location: time.UTC}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2026/regular-season", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	body := response.Body.String()
	requireElements(t, body, "button[data-standings-stat-value=goals]", "button[data-standings-stat-value=xg]")
	forbidElements(t, body, "button[data-standings-stat-value=npxg]", "[data-standings-stat-warning]")
	forbidText(t, body, "table.standings", "npxG", "Non-penalty")
}

func TestFixtureResultsShowNonPenaltyXGOnlyWhenAPenaltyHadXG(t *testing.T) {
	data := npxgSeasonData(&cache.PenaltySplit{HomeXG: .76, HomeGoals: 1, HomeShots: 1}, &cache.PenaltySplit{AwayXG: 0, HomeXG: 0, AwayShots: 1})
	groups := fixtureGroups(data, time.UTC)
	byID := map[string]fixtureView{}
	for _, group := range groups {
		for _, game := range group.Games {
			byID[game.ID] = game
		}
	}
	if got := byID["completed"].NPXG; got != "1.60–1.11" {
		t.Errorf("penalty match NPXG = %q, want 1.60–1.11", got)
	}
	if byID["completed-2"].NPXG != "" || byID["completed-2"].XG == "" {
		t.Errorf("a split with zero penalty xG must not show npxG: %+v", byID["completed-2"])
	}
	noSplit := fixtureGroups(npxgSeasonData(nil, nil), time.UTC)
	for _, group := range noSplit {
		for _, game := range group.Games {
			if game.NPXG != "" {
				t.Errorf("match without a split shows npxG %q", game.NPXG)
			}
		}
	}

	body := renderFixturesPage(t, data)
	requireText(t, body, ".fixture-xg", "xG 2.36–1.11", "xG 1.00–0.64")
	forbidText(t, body, ".fixture-xg", "npxG")
	requireText(t, body, ".fixture-npxg", "npxG 1.60–1.11")
	forbidText(t, body, ".fixture-npxg", "·")
	requireAttr(t, body, ".fixture-npxg", "title", "Non-penalty expected goals")
	if got := len(find(t, body, ".fixture-npxg")); got != 1 {
		t.Errorf("%d npxG lines, want one", got)
	}
}

func renderFixturesPage(t *testing.T, data cache.SeasonData) string {
	t.Helper()
	response := httptest.NewRecorder()
	NewHandlerWithOptions(fakeStore{season: data}, Options{Rules: testRules(30), Location: time.UTC}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2026/regular-season/fixtures", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("fixtures status = %d", response.Code)
	}
	return response.Body.String()
}

func TestBasisTextRewritesLabelsOncePerBasis(t *testing.T) {
	for _, test := range []struct{ in, want string }{
		{"Goals and xG", "Non-penalty goals and npxG"},
		{"Goal differential: Goals", "Non-penalty goal differential: Non-penalty goals"},
		{"Expected goals (xG)", "Non-penalty expected goals (npxG)"},
		{"Goals minus expected goals per match", "Non-penalty goals minus non-penalty expected goals per match"},
		{"4+ goals", "4+ non-penalty goals"},
		{"1 goal", "1 non-penalty goal"},
		{"Total goals", "Total non-penalty goals"},
		{"xG differential", "npxG differential"},
		{"Points", "Points"},
		{"xPts", "xPts"},
	} {
		if got := basisText(history.NonPenalty, test.in); got != test.want {
			t.Errorf("basisText(%q) = %q, want %q", test.in, got, test.want)
		}
		if got := basisText(history.AllShots, test.in); got != test.in {
			t.Errorf("default basis rewrote %q to %q", test.in, got)
		}
		if again := basisText(history.NonPenalty, test.want); again != test.want {
			t.Errorf("basisText is not idempotent on %q: %q", test.want, again)
		}
	}
}
