package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/apptest"
	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/history"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

const h2hSeattleID = "7vQ7BBzqD1"

func TestExploreHeadToHeadOfficialAndTable(t *testing.T) {
	t.Parallel()
	archive := apptest.Scenario(t, apptest.ScenarioTeamHistory)
	store := &archiveHTTPStore{archive: archive}
	response := httptest.NewRecorder()
	path := "/explore?view=head-to-head&opponent=" + h2hSeattleID
	NewHandler(store).ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status %d: %s", response.Code, response.Body.String())
	}
	if store.archiveCalls != 1 || store.seasonCalls != 0 || store.statusCalls != 0 {
		t.Fatal("must read one archive snapshot")
	}
	body := response.Body.String()
	requireExact(t, body, "[data-h2h-summary]", "Table: all 26 meetings")
	requireText(t, body, "[data-h2h-record]", "13 matches")
	if len(find(t, body, "[data-h2h-tile]")) != 3 || len(find(t, body, "[data-h2h-record] table, .explore-h2h-legend")) != 0 {
		t.Fatal("three stat tiles without a separate legend")
	}
	for _, venue := range []string{"All", "Home", "Away"} {
		selector := "[data-h2h-tile=" + venue + "]"
		requireText(t, body, selector, venue)
		requireText(t, body, selector, "pts/match")
		requireText(t, body, selector, "goals")
		requireText(t, body, selector, "matches")
	}
	if len(find(t, body, "[data-h2h-tile=All] i")) != 0 || len(find(t, body, "[data-h2h-tile=Home] [data-h2h-encoding=home], [data-h2h-tile=Away] [data-h2h-encoding=away]")) != 2 {
		t.Fatal("home and away tile swatches")
	}
	requireExact(t, body, "[data-h2h-direction]", "Goal margin · up: win · zero: draw · down: loss")
	requireText(t, body, "[data-h2h-scope]", "season in progress")
	if len(find(t, body, "[data-h2h-values][open]")) != 0 {
		t.Fatal("default table open")
	}
	if len(find(t, body, "[data-h2h-controls] select")) != 2 || len(find(t, body, "[data-panel=head-to-head] select[name=penalties]")) != 0 {
		t.Fatal("controls")
	}
	if len(find(t, body, "[data-h2h-title] img[alt='']")) != 2 || len(find(t, body, "[data-h2h-rows] img, [data-h2h-rows] a, [data-h2h-chart-wrap] a")) != 0 {
		t.Fatal("logos / click targets")
	}
	if len(find(t, body, "[data-h2h-rows] tr")) != 26 || len(find(t, body, "[data-h2h-fallback] li")) != 26 {
		t.Fatal("chart and table population")
	}
	code, excluded := getPage(t, archive, path+"&penalties=exclude")
	if code != http.StatusOK {
		t.Fatal("carried exclusion rejected")
	}
	requireText(t, excluded, "[data-h2h-official]", "official results, which include penalties")
	// Full Head-to-head payload and every figure are independent of the basis.
	for _, selector := range []string{"#explore-h2h-data", "[data-h2h-record]", "[data-h2h-rows]", "[data-h2h-fallback]"} {
		if text(find(t, body, selector)[0]) != text(find(t, excluded, selector)[0]) {
			t.Fatalf("basis changed %s", selector)
		}
	}
	for _, suffix := range []string{"&h2h-sort=venue", "&h2h-order=asc"} {
		code, sorted := getPage(t, archive, path+suffix)
		if code != http.StatusOK || len(find(t, sorted, "[data-h2h-values][open]")) != 1 {
			t.Fatal("sort must open table")
		}
	}
	var data exploreHeadToHeadPayload
	if err := json.Unmarshal([]byte(text(find(t, body, "#explore-h2h-data")[0])), &data); err != nil {
		t.Fatal(err)
	}
	if len(data.Teams) != 16 {
		t.Fatal("all teams must switch from payload")
	}
	payload := text(find(t, body, "#explore-h2h-data")[0])
	if len(payload) >= 300_000 {
		t.Fatalf("head-to-head payload %d bytes exceeds 300KB", len(payload))
	}
	for _, field := range []string{`"values"`, `"expected"`, `"xG"`, `"xPoints"`} {
		if strings.Contains(payload, field) {
			t.Fatalf("unused expected data: %s", field)
		}
	}
	seen := map[string]bool{}
	for _, f := range data.Fixtures {
		key := data.Seasons[f.Season].Year + "/" + f.ID
		if seen[key] {
			t.Fatalf("fixture stored twice: %s", key)
		}
		seen[key] = true
		if f.Home == f.Away {
			t.Fatal("same team fixture")
		}
	}
	summaries, err := history.SummarizeScoringBasis(archive, history.AllShots)
	if err != nil {
		t.Fatal(err)
	}
	played := 0
	for _, season := range summaries {
		if season.TeamComparisonEligible() {
			played += season.Played
		}
	}
	if len(data.Fixtures) != played {
		t.Fatalf("fixtures: %d, want %d", len(data.Fixtures), played)
	}
	// Either team's browser orientation must reproduce the reviewed Go records.
	for _, selected := range []string{portlandThornsTeamID, h2hSeattleID} {
		opponent := h2hSeattleID
		if selected == h2hSeattleID {
			opponent = portlandThornsTeamID
		}
		want := history.HeadToHead(summaries, archive, selected, opponent)
		var goalsFor, goalsAgainst int64
		count := 0
		for _, f := range data.Fixtures {
			home, away := data.Teams[f.Home].ID, data.Teams[f.Away].ID
			if home == selected && away == opponent {
				goalsFor += f.Score[0]
				goalsAgainst += f.Score[1]
				count++
			}
			if away == selected && home == opponent {
				goalsFor += f.Score[1]
				goalsAgainst += f.Score[0]
				count++
			}
		}
		if count != want.All.Played || goalsFor != want.All.GoalsFor || goalsAgainst != want.All.GoalsAgainst {
			t.Fatal("compact fixture orientation")
		}
	}

}

func TestExploreHeadToHeadDefaultsEmptyAndBadState(t *testing.T) {
	t.Parallel()
	archive := apptest.Scenario(t, apptest.ScenarioTeamHistory)
	query := url.Values{"view": {"head-to-head"}}
	summaries, err := history.SummarizeScoringBasis(archive, history.AllShots)
	if err != nil {
		t.Fatal(err)
	}
	teams, err := exploreTeams(query, summaries, archive)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := exploreTeamHistory(query, teams.TeamSeasons)
	if err != nil {
		t.Fatal(err)
	}
	page, err := exploreHeadToHead(query, summaries, archive, shared.HistoryTeam, shared.HistoryTeamOptions)
	if err != nil {
		t.Fatal(err)
	}
	if page.H2HTeam.ID != portlandThornsTeamID || page.H2HPair.All.Played != 54 {
		t.Fatal("most met opponent default")
	}
	for i, p := range page.H2HOpponents {
		if i > 0 && page.H2HOpponents[i-1].Opponent.Name > p.Opponent.Name {
			t.Fatal("name order")
		}
	}
	code, empty := getPage(t, archive, "/explore?view=head-to-head&opponent=315VnJ759x")
	if code != http.StatusOK {
		t.Fatal("valid never-met pair")
	}
	requireExact(t, empty, "[data-h2h-empty]", "Portland Thorns FC and Bay FC have not met in a regular-season match.")
	requireAttr(t, empty, "[data-h2h-opponent] option[value='315VnJ759x']", "selected", "")
	requireExact(t, empty, "[data-h2h-summary]", "Table: all 0 meetings")
	for key, good := range map[string]string{"opponent": h2hSeattleID, "h2h-sort": "margin", "h2h-order": "asc"} {
		for _, values := range [][]string{{""}, {"unknown"}, {good, good}} {
			q := url.Values{"view": {"head-to-head"}, key: values}
			code, body := getPage(t, archive, "/explore?"+q.Encode())
			if code != http.StatusBadRequest {
				t.Fatalf("%s: %d", q, code)
			}
			requireText(t, body, "h1", "Invalid Explore selection")
		}
	}
	code, _ = getPage(t, archive, "/explore?view=head-to-head&opponent="+portlandThornsTeamID)
	if code != http.StatusBadRequest {
		t.Fatal("same team")
	}
	// Preserve Explore's existing Get-based view validation, including repeats.
	for _, view := range []string{"view=", "view=head-to-head&view=unknown"} {
		code, _ = getPage(t, archive, "/explore?"+view)
		if code != http.StatusOK {
			t.Fatal("existing view validation changed")
		}
	}
}

func TestExploreHeadToHeadSortAndNewestNames(t *testing.T) {
	t.Parallel()
	archive := apptest.Scenario(t, apptest.ScenarioTeamHistory)
	for i := range archive {
		if archive[i].Entry.Season == "2026" {
			for j := range archive[i].Data.Teams {
				if archive[i].Data.Teams[j].ID == h2hSeattleID {
					archive[i].Data.Teams[j].Name = "Newest Seattle"
				}
			}
		}
	}
	code, body := getPage(t, archive, "/explore?view=head-to-head&opponent="+h2hSeattleID)
	if code != http.StatusOK {
		t.Fatal(code)
	}
	requireText(t, body, "[data-h2h-title]", "Newest Seattle")
	requireText(t, body, "[data-h2h-opponent]", "Newest Seattle (26)")
	requireText(t, body, "[data-h2h-rows]", "Seattle Reign FC")
	var payload exploreHeadToHeadPayload
	if err := json.Unmarshal([]byte(text(find(t, body, "#explore-h2h-data")[0])), &payload); err != nil {
		t.Fatal(err)
	}
	for _, season := range payload.Seasons {
		want := "Seattle Reign FC"
		if season.Year == "2026" {
			want = "Newest Seattle"
		}
		if season.Names[h2hSeattleID] != want {
			t.Fatalf("season %s opponent name %q, want %q", season.Year, season.Names[h2hSeattleID], want)
		}
	}

	for _, column := range []string{"date", "venue", "result", "margin"} {
		for _, order := range []string{"asc", "desc"} {
			rows := []exploreHeadToHeadRow{
				{exploreMatchRecord: exploreMatchRecord{Venue: "Away"}, Position: 0, Margin: -2, Points: 0},
				{exploreMatchRecord: exploreMatchRecord{Venue: "Home"}, Position: 1, Margin: 1, Points: 3},
				{exploreMatchRecord: exploreMatchRecord{Undated: true, Venue: "Away"}, Position: 2, Margin: 5, Points: 3},
			}
			sortExploreHeadToHead(rows, column, order)
			if !rows[2].Undated {
				t.Fatal("undated must remain last")
			}
			want := []int{0, 1}
			if order == "desc" {
				want = []int{1, 0}
			}
			if !reflect.DeepEqual([]int{rows[0].Position, rows[1].Position}, want) {
				t.Fatalf("sort %s %s: %+v", column, order, rows)
			}
		}
	}
	// Equal counts use the newest name, then ID, independent of archive order.
	inputs := apptest.Archive(t, map[string]apptest.ArchiveState{"2026": {Lifecycle: cache.SourceScopeActive, Goals: 3}})
	for i := range inputs[0].Data.Games {
		if i >= 10 {
			inputs[0].Data.Games[i].AwayTeamID = "charlie"
		}
	}
	inputs[0].Data.Teams = nil
	summaries, err := history.SummarizeScoringBasis(inputs, history.AllShots)
	if err != nil {
		t.Fatal(err)
	}
	options := []teamNameView{{ID: "alpha", Name: "Alpha"}, {ID: "bravo", Name: "Bravo"}, {ID: "charlie", Name: "Charlie"}}
	page, err := exploreHeadToHead(url.Values{}, summaries, inputs, options[0], options)
	if err != nil || page.H2HPair.Opponent.ID != "bravo" {
		t.Fatal("ID tie")
	}
	inputs[0].Data.Teams = append(inputs[0].Data.Teams, standings.Team{ID: "charlie", Name: "A newest name"})
	page, err = exploreHeadToHead(url.Values{}, summaries, inputs, options[0], options)
	if err != nil || !strings.HasPrefix(page.H2HPair.Opponent.Name, "A newest") {
		t.Fatal("newest name tie")
	}
}

func TestExploreHeadToHeadTeamSurvivesMissingPenaltySplits(t *testing.T) {
	t.Parallel()
	archive := apptest.Scenario(t, apptest.ScenarioNoXG)
	path := "/explore?view=head-to-head&team=" + portlandThornsTeamID + "&opponent=" + h2hSeattleID
	for _, penalties := range []string{"include", "exclude"} {
		code, body := getPage(t, archive, path+"&penalties="+penalties)
		if code != http.StatusOK {
			t.Fatalf("%s: %d", penalties, code)
		}
		requireAttr(t, body, "[data-h2h-team] option[value='"+portlandThornsTeamID+"']", "selected", "")
		requireExact(t, body, "[data-h2h-summary]", "Table: all 4 meetings")
	}
}
