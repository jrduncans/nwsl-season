package app

import (
	"html"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

func TestExploreTeamPathsAssignStableSlugs(t *testing.T) {
	saved := exploreTeamSlugs["Pk5LeeNqOW"]
	t.Cleanup(func() { exploreTeamSlugs["Pk5LeeNqOW"] = saved })
	exploreTeamSlugs["Pk5LeeNqOW"] = exploreTeamSlug{Slug: "portland-thorns", Former: []string{"portland"}}
	archive := []cache.HistoricalSeason{
		{Entry: competition.Entry{Season: "2024"}, Data: cache.SeasonData{Teams: []standings.Team{
			{ID: "Pk5LeeNqOW", Name: "Some Former Name"}, {ID: "new", Name: "Old Name FC"},
		}}},
		{Entry: competition.Entry{Season: "2025"}, Data: cache.SeasonData{
			Teams: []standings.Team{{ID: "new", Name: "Expansion Club FC"}, {ID: "clash", Name: "Portland Thorns"}, {ID: "accent", Name: "Équipe ! FC"}},
			Games: []cache.Game{{HomeTeamID: "unnamed", AwayTeamID: "new"}},
		}},
	}
	paths := newExploreTeamPaths(archive)
	for id, want := range map[string]string{
		// A fixed slug ignores the name ASA gives the team.
		"Pk5LeeNqOW": "portland-thorns",
		// Others derive from the newest name, without a trailing FC, and a
		// collision with a fixed slug is numbered.
		"new": "expansion-club", "clash": "portland-thorns-2", "accent": "quipe",
		// A team only in the games is named, and slugged, by its ID.
		"unnamed": "unnamed",
	} {
		if got, ok := paths.slug(id); !ok || got != want {
			t.Errorf("slug(%s) = %q, %v; want %q", id, got, ok, want)
		}
	}
	for segment, want := range map[string]struct {
		id               string
		current, matched bool
	}{
		"portland-thorns": {"Pk5LeeNqOW", true, true},
		"portland":        {"Pk5LeeNqOW", false, true},
		"expansion-club":  {"new", true, true},
		"old-name":        {"", false, false},
	} {
		id, current, ok := paths.resolve(segment)
		if id != want.id || current != want.current || ok != want.matched {
			t.Errorf("resolve(%s) = %q, %v, %v; want %+v", segment, id, current, ok, want)
		}
	}
	// Every fixed slug is distinct, so no two teams share a page.
	seen := map[string]string{}
	for id, slug := range exploreTeamSlugs {
		for _, name := range append([]string{slug.Slug}, slug.Former...) {
			if other, dup := seen[name]; dup {
				t.Errorf("%s and %s share slug %q", id, other, name)
			}
			if !regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`).MatchString(name) {
				t.Errorf("%s slug %q is not lowercase words joined by hyphens", id, name)
			}
			seen[name] = id
		}
	}
}

func TestExploreTeamPathsRouteAndRedirect(t *testing.T) {
	saved := exploreTeamSlugs["bravo"]
	t.Cleanup(func() {
		if saved.Slug == "" {
			delete(exploreTeamSlugs, "bravo")
		} else {
			exploreTeamSlugs["bravo"] = saved
		}
	})
	exploreTeamSlugs["bravo"] = exploreTeamSlug{Slug: "bravo", Former: []string{"old-bravo"}}
	handler := NewHandler(&archiveHTTPStore{archive: testArchive(t, map[string]archiveState{
		"2025": {Lifecycle: cache.SourceScopeCompleted, Goals: 3},
		"2026": {Lifecycle: cache.SourceScopeActive, Goals: 4},
	})})
	for _, tc := range []struct {
		path     string
		status   int
		location string
	}{
		{"/explore/season-by-season/bravo", http.StatusOK, ""},
		{"/explore/rankings/alpha?season=2025", http.StatusOK, ""},
		{"/explore/match-by-match/alpha?season=2026", http.StatusOK, ""},
		{"/explore/head-to-head/alpha", http.StatusOK, ""},
		{"/explore/head-to-head/alpha/bravo", http.StatusOK, ""},
		{"/nwsl-season/explore/head-to-head/alpha/bravo", http.StatusOK, ""},
		// The team view without a team still shows its default team.
		{"/explore/season-by-season", http.StatusOK, ""},
		// A team view's ?team= and ?opponent=, as forms submit them, move
		// into the path; the query's team wins over the path's.
		{"/explore/season-by-season?team=bravo&measure=points", http.StatusPermanentRedirect, "season-by-season/bravo?measure=points"},
		{"/explore/season-by-season/bravo?team=alpha&measure=points", http.StatusPermanentRedirect, "alpha?measure=points"},
		{"/explore/season-by-season/bravo?team=bravo", http.StatusPermanentRedirect, "bravo"},
		{"/explore/head-to-head?team=alpha&opponent=bravo&h2h-sort=venue", http.StatusPermanentRedirect, "head-to-head/alpha/bravo?h2h-sort=venue"},
		{"/explore/head-to-head/alpha?opponent=bravo", http.StatusPermanentRedirect, "alpha/bravo"},
		// A team chosen in the query drops the path's opponent.
		{"/explore/head-to-head/alpha/bravo?team=bravo&h2h-sort=venue", http.StatusPermanentRedirect, "../bravo?h2h-sort=venue"},
		{"/nwsl-season/explore/rankings?team=alpha", http.StatusPermanentRedirect, "rankings/alpha"},
		// A view named in the query redirects once, straight to the team's page.
		{"/explore?view=team-history&team=bravo&penalties=exclude", http.StatusPermanentRedirect, "explore/season-by-season/bravo?penalties=exclude"},
		{"/explore/rankings/alpha?view=parity", http.StatusPermanentRedirect, "../parity?team=alpha"},
		// A renamed team's former slug redirects to its current one.
		{"/explore/head-to-head/alpha/old-bravo?h2h-sort=venue", http.StatusPermanentRedirect, "bravo?h2h-sort=venue"},
		// Other views carry the team as a selection in the query.
		{"/explore/parity?team=alpha", http.StatusOK, ""},
		{"/explore/season-by-season/bravo/", http.StatusSeeOther, "../bravo"},
		{"/explore/season-by-season/unknown", http.StatusNotFound, ""},
		{"/explore/parity/alpha", http.StatusNotFound, ""},
		{"/explore/season-by-season/alpha/bravo", http.StatusNotFound, ""},
		{"/explore/head-to-head/alpha/bravo/alpha", http.StatusNotFound, ""},
		{"/explore/head-to-head/alpha/alpha", http.StatusBadRequest, ""},
		{"/explore/season-by-season?team=unknown", http.StatusBadRequest, ""},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if response.Code != tc.status || response.Header().Get("Location") != tc.location {
			t.Errorf("%s: status=%d location=%q, want %d %q", tc.path, response.Code, response.Header().Get("Location"), tc.status, tc.location)
		}
	}
}

// A team's page is its own canonical page, names the team in its title and
// description, and links the other team views' pages for the same team.
func TestExploreTeamPagesNameTheirTeam(t *testing.T) {
	store := &archiveHTTPStore{archive: testArchive(t, map[string]archiveState{
		"2025": {Lifecycle: cache.SourceScopeCompleted, Goals: 3},
		"2026": {Lifecycle: cache.SourceScopeActive, Goals: 4},
	})}
	public := NewHandlerWithOptions(store, Options{PublicURL: "https://example.test/"})
	for _, tc := range []struct{ path, title, description string }{
		{"/explore/season-by-season/bravo", "bravo season by season", "bravo goals, xG, points and xPoints in every NWSL season."},
		{"/explore/rankings/bravo", "bravo rankings by season", "Where bravo ranked in the NWSL each season, measure by measure."},
		{"/explore/match-by-match/bravo", "bravo match by match", "bravo results, goals and xG match by match through an NWSL season, with rolling averages."},
		{"/explore/head-to-head/bravo", "bravo head-to-head records", "Every NWSL meeting between bravo and each opponent, with results, goals and xG."},
		{"/explore/head-to-head/bravo/alpha", "bravo vs alpha head to head", "Every NWSL meeting between bravo and alpha, with results, goals and xG."},
		{"/explore/season-by-season", "NWSL team history season by season", "An NWSL team's goals, xG, points and xPoints season by season."},
	} {
		response := httptest.NewRecorder()
		public.ServeHTTP(response, httptest.NewRequest(http.MethodGet, tc.path, nil))
		body := response.Body.String()
		if response.Code != http.StatusOK {
			t.Fatalf("%s: status = %d", tc.path, response.Code)
		}
		for _, want := range []string{
			`<link rel="canonical" href="https://example.test` + tc.path + `">`,
			"<title>" + html.EscapeString(documentTitle(tc.title)) + "</title>",
			`<meta name="description" content="` + html.EscapeString(tc.description) + `">`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: missing %s", tc.path, want)
			}
		}
		if !strings.HasPrefix(tc.path, "/explore/season-by-season/") {
			continue
		}
		for _, want := range []string{`href="../rankings/bravo"`, `href="../match-by-match/bravo"`, `href="../head-to-head/bravo"`, `href="../scoring-trend"`} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: missing tab link %s", tc.path, want)
			}
		}
	}
}

func TestExploreTeamPagesListEveryTeamAndMetPair(t *testing.T) {
	archive := testArchive(t, map[string]archiveState{
		"2025": {Lifecycle: cache.SourceScopeCompleted, Goals: 3},
	})
	pages, err := exploreTeamPages(archive)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"/explore/rankings/alpha", "/explore/rankings/bravo",
		"/explore/match-by-match/alpha", "/explore/match-by-match/bravo",
		"/explore/season-by-season/alpha", "/explore/season-by-season/bravo",
		"/explore/head-to-head/alpha/bravo", "/explore/head-to-head/bravo/alpha",
	}
	if !slices.Equal(pages, want) {
		t.Errorf("pages = %q, want %q", pages, want)
	}
}

// Match pages don't load the archive: teams with a fixed slug link their
// pair's page, and others a query that redirects to it.
func TestExploreRedirectTargetWithFixedSlugsOnly(t *testing.T) {
	paths := newExploreTeamPaths(nil)
	from := "/seasons/2026/regular-season/matches/abc"
	for query, want := range map[string]string{
		"view=head-to-head&team=Pk5LeeNqOW&opponent=7vQ7BBzqD1": "../../../../explore/head-to-head/portland-thorns/seattle-reign",
		"view=head-to-head&team=alpha&opponent=bravo":           "../../../../explore/head-to-head?team=alpha&opponent=bravo",
		"view=head-to-head&team=Pk5LeeNqOW&opponent=bravo":      "../../../../explore/head-to-head/portland-thorns?opponent=bravo",
	} {
		if got := exploreRedirectTarget(from, query, paths); got != want {
			t.Errorf("%s: %q, want %q", query, got, want)
		}
	}
}
