package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/cache"
)

func contextTestTeam(id, name string, scored, allowed float64, xg ...float64) exploreTeamRecord {
	team := exploreTeamRecord{ID: id, Name: name, Played: 20, Values: [3]exploreTeamValues{{Actual: scored}, {Actual: allowed}, {Actual: scored - allowed}}}
	if len(xg) == 2 {
		difference := xg[0] - xg[1]
		team.Values[0].Expected, team.Values[1].Expected, team.Values[2].Expected = &xg[0], &xg[1], &difference
	}
	return team
}

func TestExploreContextRecordsKeepHoldersTiesAndExcludeActiveSeasons(t *testing.T) {
	seasons := []exploreTeamSeason{
		{Season: "2026", Active: true, Teams: []exploreTeamRecord{contextTestTeam("a", "New name", 10, 0, 12, 0)}},
		{Season: "2025", Teams: []exploreTeamRecord{contextTestTeam("a", "New name", 2, 1, 3, 1), contextTestTeam("b", "Beta", 2, 4, 2, 4), contextTestTeam("c", "Gamma", 1, 1)}},
		{Season: "2024"},
		{Season: "2019", Teams: []exploreTeamRecord{contextTestTeam("a", "Old name", 2, 2, 0, 1), contextTestTeam("d", "Delta", 0, 3, 2, 1)}},
	}
	metrics := exploreTeamContext(seasons)
	goals, xg := metrics[0].Goals, metrics[0].XG
	if goals.High.Value != 2 || goals.Low.Value != 0 || goals.Since != "2019" || len(goals.High.Holders) != 3 {
		t.Fatalf("completed records or ties lost: %+v / %+v", goals.High, goals.Low)
	}
	last := goals.High.Holders[2]
	if last.ID != "a" || last.Name != "Old name" || last.Season != "2019" || last.Played != 20 {
		t.Fatalf("record did not retain season identity and count: %+v", last)
	}
	if goals.Seasons[0].High.Value != 10 || !goals.Seasons[0].Active || len(goals.Seasons[1].High.Holders) != 2 || goals.Seasons[2].High != nil {
		t.Fatalf("active, tied, or absent season bounds: %+v", goals.Seasons)
	}
	if !xg.Partial || xg.Seasons[1].High != nil || xg.Seasons[1].Low != nil || xg.Seasons[1].Covered != 2 || xg.Seasons[1].Teams != 3 {
		t.Fatalf("partial xG presented as league range: %+v", xg)
	}
	if xg.High.Value != 2 || xg.Low.Value != 0 || xg.Low.Holders[0].Name != "Old name" || xg.Seasons[0].High.Value != 12 {
		t.Fatalf("xG records lost valid zero, coverage, or active exclusion: %+v", xg)
	}
	if metrics[1].Goals.High.Value != 4 || metrics[1].Goals.Low.Value != 1 || metrics[2].Goals.High.Value != 1 || metrics[2].Goals.Low.Value != -3 || metrics[2].XG.Low.Value != -1 {
		t.Fatal("allowed or signed differential extrema used the wrong measure")
	}
	if len(seasons[1].Teams) != 3 || seasons[0].Teams[0].Values[0].Actual != 10 {
		t.Fatal("context changed shared team data")
	}
}

func TestExploreContextComparesBeforeRoundingAndHandlesNoRecords(t *testing.T) {
	metrics := exploreTeamContext([]exploreTeamSeason{{Season: "2025", Teams: []exploreTeamRecord{
		contextTestTeam("a", "Alpha", 1.0001, 0), contextTestTeam("b", "Beta", 1.0004, 0),
	}}})
	goals := metrics[0].Goals
	if goals.High.Display != goals.Low.Display || goals.High.Value == goals.Low.Value || len(goals.High.Holders) != 1 || goals.High.Holders[0].ID != "b" || goals.Low.Holders[0].ID != "a" {
		t.Fatalf("rounded rates created a false tie: %+v", goals)
	}
	if metrics[0].XG.High != nil || metrics[0].XG.Low != nil || metrics[0].XG.Since != "" {
		t.Fatal("missing xG invented a record")
	}
	for _, seasons := range [][]exploreTeamSeason{nil, {{Season: "2026", Active: true, Teams: []exploreTeamRecord{contextTestTeam("a", "Alpha", 1, 0, 0, 0)}}}} {
		metrics := exploreTeamContext(seasons)
		for _, metric := range metrics {
			for _, series := range []exploreSeriesContext{metric.Goals, metric.XG} {
				if series.High != nil || series.Low != nil || series.Since != "" {
					t.Fatal("empty or active-only archive invented completed records")
				}
			}
		}
	}
}

func TestExploreContextHTTPUsesSnapshotAndPreservesControls(t *testing.T) {
	store := &historyHTTPStore{archive: historyArchive(t, map[string]historyArchiveState{
		"2019": {lifecycle: cache.SourceScopeCompleted, goals: 3, xgCovered: 20},
		"2022": {lifecycle: cache.SourceScopeCompleted, inventory: cache.InventoryCompletenessIncomplete, goals: 100},
		"2025": {lifecycle: cache.SourceScopeCompleted, goals: 3, xgCovered: 20},
		"2026": {lifecycle: cache.SourceScopeActive, goals: 10, xgCovered: 19},
	})}
	handler := NewHandler(store)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/nwsl-season/explore?view=team-history&team=alpha&measure=against&series=xg&context=on&history-sort=played", nil))
	body := response.Body.String()
	if response.Code != http.StatusOK || store.archiveCalls != 1 || store.seasonCalls != 0 {
		t.Fatalf("status=%d reads=%d/%d", response.Code, store.archiveCalls, store.seasonCalls)
	}
	_, payload, _ := strings.Cut(body, `<script type="application/json" id="explore-context-data">`)
	payload, _, _ = strings.Cut(payload, "</script>")
	var metrics [3]exploreMetricContext
	if err := json.Unmarshal([]byte(payload), &metrics); err != nil {
		t.Fatal(err)
	}
	if metrics[0].Goals.High.Value != 2 || len(metrics[0].Goals.High.Holders) != 2 || metrics[0].Goals.Seasons[2].High != nil {
		t.Fatalf("record payload included excluded results or dropped ties: %+v", metrics[0].Goals)
	}
	for _, fragment := range []string{`value="xg" selected`, `data-history-context checked`, `data-history-context-details>`, `<h4>xG allowed</h4>`, `alpha · 2019 · 20 played`, `bravo · 2025 · 20 played`, `context=on`, `series=xg`} {
		if !strings.Contains(body, fragment) {
			t.Errorf("context fallback missing %q", fragment)
		}
	}
	for _, query := range []string{"series=bad", "series=", "series=xg&series=goals", "context=", "context=true", "context=on&context=off"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/explore?view=team-history&"+query, nil))
		if response.Code != http.StatusBadRequest {
			t.Errorf("%s status=%d, want 400", query, response.Code)
		}
	}
	page, err := exploreTeamHistory(url.Values{"series": {"goals"}, "measure": {"for"}}, nil)
	if err != nil || len(page.HistoryContextDetails) != 1 || page.HistoryContextDetails[0].Label != "Goals scored" {
		t.Fatalf("context selection disagrees with chart: %+v, %v", page, err)
	}
}
