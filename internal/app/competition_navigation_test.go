package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/competition"
)

func TestCompetitionMainPageNavigation(t *testing.T) {
	for _, tc := range []struct {
		season, slug, label         string
		matchup, certified, matches bool
	}{
		{"2020", "challenge-cup-group-stage", "Group stage", true, false, true},
		{"2023", "challenge-cup-knockout-round", "Knockout round", false, false, true},
		{"2023", "challenge-cup-knockout-round", "Knockout round", true, false, false},
		{"2025", "playoffs", "Playoffs", true, false, false},
		{"2026", "playoffs", "Playoffs", false, true, false},
		{"2026", "challenge-cup-final", "Final", false, false, true},
	} {
		t.Run(tc.season+tc.slug+tc.label, func(t *testing.T) {
			entry, _ := competition.LookupSlug(tc.season, tc.slug)
			scope := requestCompetition{Season: tc.season, Stage: entry.Stage, Entry: entry, Cataloged: true}
			var games []cache.Game
			if tc.matchup {
				games = []cache.Game{{HomeTeamID: "a", AwayTeamID: "b"}}
			}
			matches := mainPageRendersMatches(scope, games, tc.certified)
			if matches != tc.matches {
				t.Fatalf("match list = %v", matches)
			}
			base := stageURL(tc.season, tc.slug)
			nav := seasonNavigationForPresentation(base, scope, base, competition.Rules{}, false, seasonPresentation{MainPageMatches: matches})
			if nav[0].Label != tc.label || !nav[0].Current {
				t.Fatalf("main item = %+v", nav[0])
			}
			for _, item := range nav {
				if matches && (item.Label == "Matches" || item.Label == "Results") {
					t.Fatal("duplicate matches item")
				}
			}
		})
	}
}

func TestMatchListFixturesRedirect(t *testing.T) {
	for _, route := range []string{
		"/seasons/2020/challenge-cup-group-stage/fixtures",
		"/seasons/2023/challenge-cup-knockout-round/fixtures",
		"/seasons/2026/challenge-cup-final/fixtures",
	} {
		response := httptest.NewRecorder()
		NewHandler(fakeStore{}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, route+"?team=a&view=upcoming", nil))
		if response.Code != http.StatusSeeOther {
			t.Fatalf("%s: %d %s", route, response.Code, response.Body.String())
		}
		want := "../" + route[len("/seasons/2020/"):len(route)-len("/fixtures")] + "?team=a&view=upcoming"
		if got := response.Header().Get("Location"); got != want {
			t.Fatalf("redirect = %s, want %s", got, want)
		}
	}
	store := handoffStore{regular: resolvedHandoffSeason()}
	response := httptest.NewRecorder()
	NewHandler(store).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2026/playoffs/fixtures", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("seeded bracket lost Matches: %d", response.Code)
	}
}
