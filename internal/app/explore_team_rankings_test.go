package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/cache"
)

func TestExploreTeamRankingsDirectionTiesAndUnits(t *testing.T) {
	teams := []exploreTeamRecord{
		{ID: "a", Played: 2}, {ID: "b", Played: 4}, {ID: "c", Played: 1}, {ID: "d", Played: 1},
	}
	actual := [][3]float64{{4, 2, 2}, {4, 1, 3}, {2, 3, -1}, {0, 0, 0}}
	expected := [][3]float64{{3, 1, 2}, {4, 0, 4}, {2, 2, 0}, {0, 0, 0}}
	for i := range teams {
		for measure := range 3 {
			teams[i].Values[measure] = exploreTeamValues{Actual: actual[i][measure], Expected: &expected[i][measure]}
			total := expected[i][measure] * float64(teams[i].Played)
			teams[i].Totals[measure] = exploreTeamValues{Actual: actual[i][measure] * float64(teams[i].Played), Expected: &total}
		}
	}
	populateExploreTeamRanks(teams)
	want := [][6]string{
		{"Tied 1st", "3rd", "2nd", "2nd", "3rd", "2nd"},
		{"Tied 1st", "2nd", "1st", "1st", "Tied 1st", "1st"},
		{"3rd", "4th", "4th", "3rd", "4th", "Tied 3rd"},
		{"4th", "1st", "3rd", "4th", "Tied 1st", "Tied 3rd"},
	}
	for i, team := range teams {
		for metric, rank := range want[i] {
			if team.Rankings[0][metric].Rank != rank {
				t.Errorf("%s metric %d rank = %s, want %s", team.ID, metric, team.Rankings[0][metric].Rank, rank)
			}
		}
	}
	if teams[0].Rankings[1][0].Rank != "2nd" || teams[1].Rankings[1][0].Rank != "1st" || teams[0].Rankings[1][0].Value != "8" {
		t.Fatalf("totals failed to change ranks with uneven games: %+v", teams)
	}
	if teams[0].Rankings[0][1].Position != "66.6667" || teams[2].Rankings[0][1].Position != "100.0000" || teams[3].Rankings[0][1].Position != "0.0000" {
		t.Fatal("allowed ranks do not map best to worst on the visual scale")
	}
	page := exploreTeamRankings(exploreTeamsView{TeamSeason: "2026", TeamUnits: "total", TeamSeasons: []exploreTeamSeason{{Season: "2026", Active: true, Teams: teams}}}, teamNameView{ID: "a"})
	if len(page.RankingRows) != 6 || page.RankingRows[0].Rank != "2nd" || page.RankingPlayed != 2 || page.RankingTeamCount != 4 || !page.RankingActive {
		t.Fatalf("selected summary: %+v", page)
	}
}

func TestExploreTeamRankingsFullPrecisionAndMissingLeagueXG(t *testing.T) {
	xg := 0.0
	teams := []exploreTeamRecord{{ID: "a"}, {ID: "b"}}
	for i := range teams {
		for measure := range 3 {
			teams[i].Values[measure].Expected = &xg
		}
	}
	teams[0].Values[0].Actual, teams[1].Values[0].Actual = 1.004, 1.003
	populateExploreTeamRanks(teams)
	if teams[0].Rankings[0][0].Value != teams[1].Rankings[0][0].Value || teams[0].Rankings[0][0].Rank != "1st" || teams[1].Rankings[0][0].Rank != "2nd" {
		t.Fatal("rank must use values before display rounding")
	}
	for measure := range 3 {
		teams[1].Values[measure].Expected = nil
	}
	populateExploreTeamRanks(teams)
	for _, team := range teams {
		for _, row := range team.Rankings[0][3:] {
			if row.Rank != "Unavailable" || row.Position != "" {
				t.Errorf("partial league xG was ranked: %+v", row)
			}
		}
	}
	if teams[0].Rankings[0][3].Value != "0.00" || teams[1].Rankings[0][3].Value != "Unavailable" || teams[0].Rankings[0][0].Rank != "1st" {
		t.Fatal("missing league xG must preserve covered team values and goal ranks")
	}
	populateExploreTeamRanks(teams[:1])
	if teams[0].Rankings[0][3].Rank != "1st" || teams[0].Rankings[0][3].Position != "0.0000" {
		t.Fatal("single team or zero xG mishandled")
	}
	for rank, want := range map[int]string{1: "1st", 2: "2nd", 3: "3rd", 10: "10th", 11: "11th", 12: "12th", 13: "13th", 16: "16th", 21: "21st"} {
		if got := ordinalRank(rank); got != want {
			t.Errorf("ordinal %d = %s, want %s", rank, got, want)
		}
	}
}

func TestExploreTeamRankingsFallbackAndEmptySelections(t *testing.T) {
	store := &historyHTTPStore{archive: historyArchive(t, map[string]historyArchiveState{
		"2024": {lifecycle: cache.SourceScopeUpcoming},
		"2025": {lifecycle: cache.SourceScopeCompleted, goals: 3, xgCovered: 19},
		"2026": {lifecycle: cache.SourceScopeActive, goals: 2, xgCovered: 20},
	})}
	for _, tc := range []struct {
		query string
		want  []string
	}{
		{"season=2026&team=alpha", []string{`data-panel="team-rankings" aria-labelledby=`, `data-view-choice="team-rankings" aria-current="page"`, `<input type="hidden" name="view" value="team-rankings">`, `2026 · 20 played · 2 teams · In progress`, `Tied 1st`, ` per match`, `style="left:0.0000%"`, `data-rankings-warning hidden`}},
		{"season=2025&team=alpha&units=total", []string{`data-rankings-warning>`, `<option value="total" selected>Totals`, ` total`, `League xG ranks are unavailable`, `Unavailable`}},
		{"season=2024&team=alpha", []string{`data-rankings-empty>`, `data-rankings-results hidden`}},
	} {
		t.Run(tc.query, func(t *testing.T) {
			response := httptest.NewRecorder()
			NewHandler(store).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/nwsl-season/explore?view=team-rankings&"+tc.query, nil))
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d: %s", response.Code, response.Body.String())
			}
			_, panel, _ := strings.Cut(response.Body.String(), `<section data-panel="team-rankings"`)
			panel, _, _ = strings.Cut(panel, `</section>`)
			for _, want := range tc.want {
				if !strings.Contains(response.Body.String(), want) {
					t.Errorf("missing %q", want)
				}
			}
			if !strings.Contains(tc.query, "2024") {
				if strings.Count(panel, `<article class="explore-rank-card">`) != 6 {
					t.Errorf("six fallback cards missing: %s", panel)
				}
			}
		})
	}
	for _, query := range []string{"team=unknown", "team=", "team=alpha&team=beta", "units=bad", "units=total&units=total", "season=1900", "season=2026&season=2025"} {
		response := httptest.NewRecorder()
		NewHandler(store).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/explore?view=team-rankings&"+query, nil))
		if response.Code != http.StatusBadRequest {
			t.Errorf("query %s status=%d", query, response.Code)
		}
	}
	if store.seasonCalls != 0 {
		t.Fatal("rankings fetched individual season data")
	}
}
