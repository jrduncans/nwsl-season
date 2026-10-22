package app

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"

	"github.com/jrduncans/nwsl-season/internal/apptest"
	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/history"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

func penaltyArchive(t *testing.T, splits int) []cache.HistoricalSeason {
	t.Helper()
	return testArchive(t, map[string]archiveState{
		"2025": {Lifecycle: cache.SourceScopeCompleted, Goals: 3, XGCovered: 20, PenaltySplits: splits},
	})
}

func getPage(t *testing.T, archive []cache.HistoricalSeason, target string) (int, string) {
	t.Helper()
	response := httptest.NewRecorder()
	NewHandlerWithOptions(&archiveHTTPStore{archive: archive}, Options{ForecastIterations: 20}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
	return response.Code, response.Body.String()
}

func TestPenaltiesParameterIsRejectedLikeAnInvalidMeasure(t *testing.T) {
	archive := penaltyArchive(t, 20)
	for _, target := range []string{
		"/explore?view=teams&penalties=bogus", "/explore?penalties=", "/explore?penalties=include&penalties=exclude",
		"/explore?view=trend&penalties=Exclude",
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
	requireExact(t, body, "main h1", "Invalid Explore selection")
	_, penalties := getPage(t, archive, "/explore?penalties=bogus")
	requireExact(t, penalties, "main h1", "Invalid Explore selection")
}

func TestPenaltiesDefaultBasisKeepsLinksAndOnlyAddsTheControl(t *testing.T) {
	archive := penaltyArchive(t, 20)
	_, plain := getPage(t, archive, "/explore?view=team-history&team=alpha&season=2025")
	_, included := getPage(t, archive, "/explore?view=team-history&team=alpha&season=2025&penalties=include")
	// The explicit default differs only in the echoed selection state.
	forbidElements(t, plain, `input[data-penalties-selector][checked]`)
	forbidElements(t, included, `input[data-penalties-selector][checked]`)
	if plain != included {
		t.Error("penalties=include must render exactly as an absent parameter")
	}
	forbidRaw(t, plain, "npxG", `data-points-note`, `data-penalties="exclude"`)
	forbidText(t, plain, `[data-panel="team-history"]`, "Non-penalty")
	requireAttr(t, plain, `th[data-h2h-basis-column]`, "hidden", "")
	// The checkbox is last in each view's control row and unchecked by default.
	if got := len(find(t, plain, `input[data-penalties-selector][name=penalties][value=exclude]`)); got != 9 {
		t.Errorf("default page has %d penalties checkboxes, want 9", got)
	}
	// Default forms submit no penalties parameter.
	for _, form := range find(t, plain, `form`) {
		if got := submittedPenalties(t, plain, form); got != 0 {
			t.Errorf("default form %q submits %d penalties values", attr(form, "class"), got)
		}
	}
	requireText(t, plain, "form[data-team-history-controls] label.penalties-select", "Exclude penalties")
	requireAttr(t, plain, `input[data-penalties-selector]`, "title", "Excluding removes penalty kicks from both goals and xG.")
	forbidRaw(t, plain, "explore-penalties", "data-penalties-control", "Penalty kicks excluded.")
	if got := len(find(t, plain, `nav input[data-penalties-selector]`)); got != 0 {
		t.Errorf("%d penalties checkboxes in navigation", got)
	}
}

// submittedPenalties counts the penalties values a native submission of form
// sends: enabled hidden fields and checked, enabled checkboxes.
func submittedPenalties(t *testing.T, body string, form *html.Node) int {
	t.Helper()
	count := 0
	inputs := findIn(t, form, `input[name=penalties]`)
	if id := attr(form, "id"); id != "" {
		inputs = append(inputs, find(t, body, `input[name=penalties][form=`+id+`]`)...)
	}
	for _, input := range inputs {
		if _, disabled := lookupAttr(input, "disabled"); disabled {
			continue
		}
		if _, checked := lookupAttr(input, "checked"); attr(input, "type") == "checkbox" && !checked {
			continue
		}
		count++
	}
	return count
}

// Views without another selection form wrap the checkbox in a GET form that
// carries the rest of the selection, so it works without JavaScript.
func TestExplorePenaltiesCheckboxSubmitsWithoutJavaScript(t *testing.T) {
	archive := penaltyArchive(t, 20)
	for _, panel := range []string{"trend", "distribution", "home-advantage", "table"} {
		t.Run(panel, func(t *testing.T) {
			_, body := getPage(t, archive, "/explore?view="+panel+"&metric=gap&penalties=exclude")
			forms := find(t, body, `section[data-panel=`+panel+`] form[method=get]`)
			var form *html.Node
			for _, candidate := range forms {
				if len(findIn(t, candidate, `input[data-penalties-selector][checked]`)) == 1 {
					form = candidate
				}
			}
			if form == nil {
				t.Fatalf("%d GET forms, none holds the checked checkbox", len(forms))
			}
			fields := map[string]string{}
			for _, input := range findIn(t, form, `input[type=hidden]`) {
				fields[attr(input, "name")] = attr(input, "value")
			}
			if fields["view"] != panel {
				t.Errorf("form carries view %q", fields["view"])
			}
			if panel != "distribution" && fields["metric"] != "gap" {
				t.Errorf("form dropped the selection: %v", fields)
			}
			if got := submittedPenalties(t, body, form); got != 1 {
				t.Errorf("checked form submits %d penalties values, want 1", got)
			}
			submit := false
			for _, noscript := range findIn(t, form, `noscript`) {
				submit = submit || noscript.FirstChild != nil && strings.Contains(noscript.FirstChild.Data, `type="submit"`)
			}
			if !submit {
				t.Error("form has no submit button without JavaScript")
			}
		})
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
		t.Fatalf("points and xPoints changed with the basis: %+v vs %+v", alpha.Totals[3], official.Totals[3])
	}
	if page.TeamActualLabel != "Non-penalty goal differential" || page.TeamXGLabel != "npxG differential" {
		t.Fatalf("labels = %q / %q", page.TeamActualLabel, page.TeamXGLabel)
	}
	if !strings.Contains(page.TeamDisplayURLs["chart"], "penalties=exclude") || !strings.Contains(page.TeamDisplayURLs["table"], "penalties=exclude") {
		t.Fatalf("selection links lost the basis: %s %s", page.TeamDisplayURLs["chart"], page.TeamDisplayURLs["table"])
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
			panel := strings.TrimPrefix(strings.SplitN(view, "&", 2)[0], "view=")
			if got := len(find(t, body, `section[data-panel=`+panel+`] label.penalties-select`)); got != 1 {
				t.Errorf("the %s view offers %d Penalties checkboxes, want 1", panel, got)
			}
			requireElements(t, body, `section[data-panel=`+panel+`] input[data-penalties-selector][checked]`)
			switch panel {
			case "trend", "table":
			case "season-trend":
				// Match by match states the basis in its status line instead.
				requireText(t, body, "[data-trend-season-status]", "· non-penalty")
				forbidText(t, body, "section[data-panel=season-trend]", "Penalty kicks excluded.")
			default:
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
			// Every selection form submits the basis exactly once: through its
			// checkbox or, without one, a hidden field. Only a disabled
			// checkbox (a points-only selection) submits none.
			forms := find(t, body, `form.explore-team-controls`)
			if len(forms) == 0 {
				t.Fatal("no selection forms")
			}
			for _, form := range forms {
				want := 1
				if len(findIn(t, form, `input[name=penalties]`)) == len(findIn(t, form, `input[name=penalties][disabled]`)) && len(findIn(t, form, `input[name=penalties]`)) > 0 {
					want = 0
				}
				if got := submittedPenalties(t, body, form); got != want {
					t.Errorf("form %q submits %d penalties values, want %d", attr(form, "data-team-controls")+attr(form, "class"), got, want)
				}
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

	// The points note appears wherever points or xPoints are shown, only on the
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
		"/explore?view=teams&display=chart&measure=points&penalties=exclude":                      false,
		"/explore?view=teams&display=table&penalties=exclude":                                     true,
		"/explore?view=teams&display=chart&measure=for&penalties=exclude":                         false,
		"/explore?view=teams&display=quadrant&measure=points&penalties=exclude":                   false,
		"/explore?view=team-history&measure=points&penalties=exclude":                             false,
		"/explore?view=season-trend&trend-view=points&penalties=exclude":                          false,
		"/explore?view=season-trend&trend-view=relative&average-series=xpoints&penalties=exclude": false,
		"/explore?view=team-history&penalties=exclude":                                            true,
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

func TestExplorePointsOnlySelectionsIgnoreAndCanonicalizePenalties(t *testing.T) {
	archive := penaltyArchive(t, 20)
	for _, selection := range apptest.ExplorePointsOnlyCases() {
		if !selection.PointsOnly {
			continue
		}
		path := "/explore"
		if selection.Query != "" {
			path += "?" + selection.Query
		}
		separator := "?"
		if strings.Contains(path, "?") {
			separator = "&"
		}
		path += separator + "penalties=exclude"
		_, body := getPage(t, archive, path)
		t.Run(path, func(t *testing.T) {
			panel := strings.Split(path, "view=")[1]
			panel = strings.Split(panel, "&")[0]
			panelSelector := "[data-panel=" + panel + "]"
			controlSelector := panelSelector + " label.penalties-select[hidden]"
			if panel == "head-to-head" {
				controlSelector = panelSelector + " [data-h2h-penalties-label][hidden]"
			}
			if panel == "team-seasons" {
				controlSelector = panelSelector + " [data-team-seasons-penalties-control][hidden]"
			}
			if len(find(t, body, controlSelector)) == 0 {
				t.Fatalf("Penalties control is not hidden in %s", panel)
			}
			for _, note := range find(t, body, `[data-points-note]`) {
				if note.Parent != nil && note.Parent.Data == "section" && attr(note.Parent, "data-panel") != panel {
					continue
				}
				if _, hidden := lookupAttr(note, "hidden"); !hidden {
					t.Errorf("points note is visible")
				}
			}
			for _, form := range find(t, body, panelSelector+" form") {
				if len(findIn(t, form, `input[type=hidden][name=penalties]`)) != 0 {
					t.Errorf("form carries stale penalties: %s", text(form))
				}
			}
			for _, link := range find(t, body, "a[href]") {
				if strings.Contains(attr(link, "href"), "penalties=") {
					t.Errorf("link carries penalties: %s", attr(link, "href"))
				}
			}
		})
	}
}

func TestPointsOnlyExplorePredicateMatchesSharedCases(t *testing.T) {
	for _, selection := range apptest.ExplorePointsOnlyCases() {
		query, err := url.ParseQuery(selection.Query)
		if err != nil {
			t.Fatalf("parse shared query %q: %v", selection.Query, err)
		}
		if got := pointsOnlyExploreSelection(query); got != selection.PointsOnly {
			t.Errorf("pointsOnlyExploreSelection(%q) = %v, want %v", selection.Query, got, selection.PointsOnly)
		}
	}
}

func TestWithoutQueryParameterPreservesRemainingOrder(t *testing.T) {
	got := withoutQueryParameter("z=last&%70enalties=exclude&a=first&empty=&z=again", "penalties")
	if want := "z=last&a=first&empty=&z=again"; got != want {
		t.Fatalf("withoutQueryParameter() = %q, want %q", got, want)
	}
}

func TestExplorePenaltiesIncompleteSeasonIsExcludedWithTheNonPenaltyReason(t *testing.T) {
	archive := penaltyArchive(t, 19)
	code, body := getPage(t, archive, "/explore?view=table&penalties=exclude")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	requireText(t, body, "[data-panel=table]", "No seasons currently have enough complete results to compare.")
	_, teams := getPage(t, archive, "/explore?view=teams&penalties=exclude")
	requireElements(t, teams, `[data-team-empty]`)
	forbidText(t, teams, "#explore-team-data", `"alpha"`)
	// The default basis is unaffected by the missing split.
	_, defaults := getPage(t, archive, "/explore?view=table")
	requireRowText(t, defaults, "[data-panel=table] tbody tr", "2025", "20", "3.00", "1.00")
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
		{"xPoints", "xPoints"},
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
