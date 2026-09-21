package app

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

// TestHistoryPreview is an opt-in loopback harness for the packet's browser
// checks. Ordinary test runs return immediately; an explicit output path keeps
// preview state out of production code and the normal test suite.
func TestHistoryPreview(t *testing.T) {
	if os.Getenv("NWSL_HISTORY_PREVIEW") == "" {
		return
	}
	const outputPath = "/private/tmp/nwsl-season-h05-preview-url"

	states := map[string]historyArchiveState{
		"2018": {lifecycle: cache.SourceScopeCompleted, inventory: cache.InventoryCompletenessComplete, goals: 2, xgCovered: 20},
		"2019": {lifecycle: cache.SourceScopeCompleted, goals: 3, xgCovered: 19},
		"2021": {lifecycle: cache.SourceScopeActive, goals: 2, xgCovered: 20},
		"2022": {lifecycle: cache.SourceScopeCompleted, inventory: cache.InventoryCompletenessIncomplete, goals: 1},
	}
	archive := historyArchive(t, states)
	selection := "2022"
	switch os.Getenv("NWSL_HISTORY_PREVIEW_SCENARIO") {
	case "teams", "team-history":
		archive = historyArchive(t, map[string]historyArchiveState{
			"2024": {lifecycle: cache.SourceScopeUpcoming},
			"2025": {lifecycle: cache.SourceScopeCompleted, goals: 3},
			"2026": {lifecycle: cache.SourceScopeActive, goals: 3},
		})
		if os.Getenv("NWSL_HISTORY_PREVIEW_SCENARIO") == "team-history" {
			for _, year := range []string{"2016", "2017", "2018", "2019", "2021", "2022", "2023"} {
				archive = append(archive, historyArchive(t, map[string]historyArchiveState{
					year: {lifecycle: cache.SourceScopeCompleted, goals: 3},
				})...)
			}
		}
		for i := range archive {
			season := &archive[i]
			if season.Entry.Season == "2024" {
				season.Data.Games = nil
				continue
			}
			season.Data.Teams = []standings.Team{
				{ID: "kRQa8JOqKZ", Name: "Angel City FC"}, {ID: "315VnJ759x", Name: "Bay FC"},
				{ID: "odMX2OJqYL", Name: "Boston Legacy FC"}, {ID: "KPqjw8PQ6v", Name: "Chicago Stars FC"},
				{ID: "2lqRn34qr0", Name: "Denver Summit FC"}, {ID: "raMyrr25d2", Name: "Gotham FC"},
				{ID: "4JMAk47qKg", Name: "Houston Dash"}, {ID: "4wM4rZdqjB", Name: "Kansas City Current"},
				{ID: "zeQZeazqKw", Name: "North Carolina Courage"}, {ID: "XVqKeVKM01", Name: "Orlando Pride"},
				{ID: "Pk5LeeNqOW", Name: "Portland Thorns FC"}, {ID: "eV5DR6YQKn", Name: "Racing Louisville FC"},
				{ID: "7VqG1lYMvW", Name: "San Diego Wave FC"}, {ID: "7vQ7BBzqD1", Name: "Seattle Reign FC"},
				{ID: "eV5D2w9QKn", Name: "Utah Royals FC"}, {ID: "aDQ0lzvQEv", Name: "Washington Spirit"},
			}
			season.Data.Games = historyGames(season.Entry.Season, 64, 3)
			for j := range season.Data.Games {
				game := &season.Data.Games[j]
				game.HomeTeamID, game.AwayTeamID = season.Data.Teams[j%16].ID, season.Data.Teams[(j+5)%16].ID
				game.HomeScore.Int64, game.AwayScore.Int64 = int64(j%4), int64((j/3)%3)
				if os.Getenv("NWSL_HISTORY_PREVIEW_SCENARIO") == "team-history" {
					year := int(season.Entry.Season[3] - '0')
					game.HomeScore.Int64, game.AwayScore.Int64 = int64((j+year)%5), int64((j/3+year)%3)
				}
				if (season.Entry.Season == "2026" || season.Entry.Season == "2022") && j == 1 {
					continue
				}
				season.Data.XGoals = append(season.Data.XGoals, cache.GameXG{
					GameID: game.ASAID, Availability: cache.XGAvailable, HomeTeamID: game.HomeTeamID, AwayTeamID: game.AwayTeamID,
					HomeXG: sql.NullFloat64{Float64: .5 + float64(j%7)*.3, Valid: true}, AwayXG: sql.NullFloat64{Float64: .2 + float64(j%5)*.25, Valid: true},
				})
			}
		}
	case "overview":
		states = make(map[string]historyArchiveState)
		for _, year := range []string{"2016", "2017", "2018", "2019", "2021", "2022", "2023", "2024", "2025", "2026"} {
			states[year] = historyArchiveState{lifecycle: cache.SourceScopeCompleted, goals: 3, xgCovered: 20}
		}
		states["2026"] = historyArchiveState{lifecycle: cache.SourceScopeActive, inventory: cache.InventoryCompletenessComplete, goals: 3, xgCovered: 20}
		archive = historyArchive(t, states)
		for i := range archive {
			for j := range archive[i].Data.Games {
				game := &archive[i].Data.Games[j]
				// Include all five bins while varying each season deterministically.
				total := int64((j + int(archive[i].Entry.Season[len(archive[i].Entry.Season)-1])) % 6)
				game.HomeScore.Int64, game.AwayScore.Int64 = total/2, total-total/2
				archive[i].Data.XGoals[j].HomeXG.Float64 = 1.2 + float64(j%3)*0.1
				archive[i].Data.XGoals[j].AwayXG.Float64 = 1.1
			}
		}
		selection = "2025"
	case "empty":
		archive = historyArchive(t, map[string]historyArchiveState{"2024": {lifecycle: cache.SourceScopeActive, goals: 1}})
		archive[0].Data.Games = historyGames("2024", 19, 1)
		selection = "2024"
	case "single":
		archive = historyArchive(t, map[string]historyArchiveState{"2024": {lifecycle: cache.SourceScopeCompleted, goals: 1}})
		selection = "2024"
	}

	handler := NewHandler(&historyHTTPStore{archive: archive})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if os.Getenv("NWSL_HISTORY_PREVIEW_NO_SCRIPT") != "" {
			w.Header().Set("Content-Security-Policy", "script-src 'none'")
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	metric := os.Getenv("NWSL_HISTORY_PREVIEW_METRIC")
	if metric == "" {
		metric = "xg"
	}
	if err := os.WriteFile(outputPath, []byte(server.URL+"/nwsl-season/history/scoring?metric="+metric+"&season="+selection), 0o600); err != nil { // #nosec G703 -- fixed test-only preview path
		t.Fatal(err)
	}
	timer := time.NewTimer(5 * time.Minute)
	defer timer.Stop()
	<-timer.C
}
