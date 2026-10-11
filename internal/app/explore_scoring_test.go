package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/apptest"
	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/history"
)

// archiveHTTPStore serves one historical archive and counts reads, so tests
// can prove Explore uses a single archive read and never per-season reads.
type archiveHTTPStore struct {
	archive                                []cache.HistoricalSeason
	err                                    error
	archiveCalls, seasonCalls, statusCalls int
}

func (s *archiveHTTPStore) HistoricalRegularSeasons(context.Context) ([]cache.HistoricalSeason, error) {
	s.archiveCalls++
	return s.archive, s.err
}

func (s *archiveHTTPStore) Season(context.Context, string, string) (cache.SeasonData, error) {
	s.seasonCalls++
	return cache.SeasonData{}, errors.New("unexpected season read")
}

func (s *archiveHTTPStore) Status(context.Context, string, string) (cache.Status, error) {
	s.statusCalls++
	return cache.Status{}, errors.New("unexpected status read")
}

// The archive fixtures live in internal/apptest so browser tests and
// cmd/preview share them.
type archiveState = apptest.ArchiveState

func testArchive(t *testing.T, states map[string]archiveState) []cache.HistoricalSeason {
	t.Helper()
	return apptest.Archive(t, states)
}

func TestLegacyHistoryRedirectsToScoringTrend(t *testing.T) {
	handler := NewHandler(&archiveHTTPStore{})
	tests := []struct{ path, want string }{
		{"/history", "explore?view=trend"},
		{"/history/scoring", "../explore?view=trend"},
		{"/history/scoring?season=2025&metric=xg&penalties=exclude", "../explore?metric=xg&penalties=exclude&view=trend"},
	}
	for _, test := range tests {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
		if response.Code != http.StatusMovedPermanently || response.Header().Get("Location") != test.want {
			t.Errorf("%s = %d %q, want 301 %q", test.path, response.Code, response.Header().Get("Location"), test.want)
		}
	}
}

func TestExploreErrorsRenderExploreNavigation(t *testing.T) {
	handler := NewHandler(&archiveHTTPStore{err: errors.New("disk")})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/explore", nil))
	body := response.Body.String()
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", response.Code)
	}
	requireExact(t, body, "main h1", "Explore unavailable")
	requireAttr(t, body, `nav a[aria-current=page]`, "href", "explore")
}

func TestScoringPageTrendStatus(t *testing.T) {
	rate := func(value float64) *float64 { return &value }
	page := exploreScoringPageFor("/explore", []history.SeasonScoring{
		{Season: "2024", Played: 182, PlotEligible: true, Lifecycle: cache.SourceScopeCompleted, GoalsPerMatch: rate(2.7), XGPerMatch: rate(2.6)},
		{Season: "2025", Played: 150, PlotEligible: true, Lifecycle: cache.SourceScopeCompleted, GoalsPerMatch: rate(2.8)},
		{Season: "2026", Played: 40, PlotEligible: true, Lifecycle: cache.SourceScopeActive, GoalsPerMatch: rate(2.9), XGPerMatch: rate(2.5)},
		{Season: "2027", Played: 3, Lifecycle: cache.SourceScopeActive, GoalsPerMatch: rate(4)},
	}, history.AllShots)
	if !page.HasTrendData || page.ActiveStatus != "2026: in progress through 40 matches" || !page.XGIncomplete {
		t.Fatalf("trend status = %v %q %v", page.HasTrendData, page.ActiveStatus, page.XGIncomplete)
	}
	if empty := exploreScoringPageFor("/explore", nil, history.AllShots); empty.HasTrendData || empty.XGIncomplete || empty.HasDistributionBars {
		t.Fatalf("empty archive status = %+v", empty)
	}
}

func TestDistributionUsesExactCountsAndAccessiblePercentages(t *testing.T) {
	distribution := exploreDistributionSeasonFor(history.SeasonScoring{Season: "2024", Played: 20, PlotEligible: true, GoalBins: [5]int{4, 4, 4, 4, 4}})
	for _, segment := range distribution.Segments {
		if segment.Percent != "20.0%" || segment.Width != "20" || !segment.HasWidth {
			t.Errorf("segment = %+v, want exact 20%%", segment)
		}
	}
	if !strings.Contains(distribution.AccessibleName, "0 goals: 4 matches (20.0%)") || !strings.Contains(distribution.AccessibleName, "4+ goals: 4 matches (20.0%)") {
		t.Fatalf("distribution accessible name = %q", distribution.AccessibleName)
	}

	thirds := exploreDistributionSeasonFor(history.SeasonScoring{Season: "2025", Played: 3, PlotEligible: true, GoalBins: [5]int{1, 1, 1, 0, 0}})
	for _, segment := range thirds.Segments[:3] {
		if segment.Percent != "33.3%" {
			t.Errorf("third segment = %+v, want 33.3%%", segment)
		}
	}
	if thirds.Segments[3].HasWidth || thirds.Segments[4].HasWidth {
		t.Errorf("zero bins received visible widths: %+v", thirds.Segments)
	}
}

func TestDistributionCoversEmptyAndMiddleBins(t *testing.T) {
	for name, bins := range map[string][5]int{
		"only zero goals":      {20, 0, 0, 0, 0},
		"only four plus goals": {0, 0, 0, 0, 20},
		"zeros in middle bins": {4, 0, 4, 0, 12},
	} {
		t.Run(name, func(t *testing.T) {
			segments := exploreDistributionSeasonFor(history.SeasonScoring{Season: "2024", Played: 20, PlotEligible: true, GoalBins: bins}).Segments
			for index, count := range bins {
				if segments[index].Count != count {
					t.Fatalf("bin %d count=%d, want %d", index, segments[index].Count, count)
				}
				if count == 0 && segments[index].HasWidth {
					t.Errorf("zero bin %d has visible width: %+v", index, segments[index])
				}
			}
		})
	}
}

func TestExploreLinksStylesheetAndHome(t *testing.T) {
	_, body := getPage(t, penaltyArchive(t, 20), "/explore")
	requireAttr(t, body, `link[rel=stylesheet]`, "href", staticURL("/explore", "site.css"))
	requireAttr(t, body, `a.brand`, "href", relativeURL("/explore", "/"))
}
