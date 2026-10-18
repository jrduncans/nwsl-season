package app

import (
	"net/http"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/cache"
)

func TestExploreSeasonBalanceTilesAndRolling(t *testing.T) {
	t.Parallel()
	archive := testArchive(t, map[string]archiveState{"2025": {Lifecycle: cache.SourceScopeCompleted, Goals: 3, XGCovered: 19}})
	path := "/explore?view=season-trend&team=alpha&season=2025&trend-view=balance"
	code, body := getPage(t, archive, path+"&smoothing=none")
	if code != http.StatusOK {
		t.Fatal(code)
	}
	forbidElements(t, body, "[data-trend-margin-tiles][hidden]")
	if len(find(t, body, "[data-trend-margin-tiles] [data-h2h-tile]")) != 3 {
		t.Fatal("expected three venue record tiles without JavaScript")
	}
	requireAttr(t, body, "[data-season-trend-note]", "hidden", "")
	requireText(t, body, "[data-trend-margin-tiles]", "pts/match", "goals", "xG incomplete", "xG 19/20 matches", "20 matches")
	requireText(t, body, "[data-trend-headings]", "Points", "xG for", "xG against", "xPoints")
	if len(find(t, body, "[data-trend-headings] th")) != 10 {
		t.Fatal("Scoring balance must have all per-match columns, without derived columns")
	}
	_, rolling := getPage(t, archive, path+"&smoothing=5")
	requireAttr(t, rolling, "[data-trend-margin-tiles]", "hidden", "")
}
