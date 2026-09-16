package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
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
