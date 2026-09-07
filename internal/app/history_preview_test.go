package app

import (
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
	case "empty":
		archive = historyArchive(t, map[string]historyArchiveState{"2024": {lifecycle: cache.SourceScopeActive, goals: 1}})
		archive[0].Data.Games = historyGames("2024", 19, 1)
		selection = "2024"
	case "single":
		archive = historyArchive(t, map[string]historyArchiveState{"2024": {lifecycle: cache.SourceScopeCompleted, goals: 1}})
		selection = "2024"
	}

	server := httptest.NewServer(NewHandler(&historyHTTPStore{archive: archive}))
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
