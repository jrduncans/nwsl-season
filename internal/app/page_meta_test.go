package app

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jrduncans/nwsl-season/internal/competition"
)

func TestDocumentTitleDropsTheSiteNameWhenItWouldNotFit(t *testing.T) {
	if got := documentTitle("2026 NWSL standings"); got != "2026 NWSL standings · NWSL by the Numbers" {
		t.Errorf("short title = %q", got)
	}
	long := "Washington Spirit 2–1 Kansas City Current, 2026 NWSL playoffs"
	if got := documentTitle(long); got != long {
		t.Errorf("long title = %q, want it without the site name", got)
	}
}

func TestMatchMetaNamesTheMeeting(t *testing.T) {
	playoffs := requestCompetition{Season: "2026", Stage: "Playoffs"}
	regular := requestCompetition{Season: "2026", Stage: "Regular Season"}
	spirit, current := teamNameView{Name: "Washington Spirit"}, teamNameView{Name: "Kansas City Current"}
	played := func(score, winner string) fixtureView {
		return fixtureView{HomeTeam: spirit, AwayTeam: current, KickoffUTC: "2026-11-16T01:00:00Z", Completed: true, Score: score, Winner: winner,
			XGLines: []matchLine{{Label: "xG", Home: "1.84", Away: "0.91"}}}
	}
	eastern, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name               string
		scope              requestCompetition
		match              fixtureView
		outlook            bool
		title, description string
	}{
		{"home win", playoffs, played("2–1", "home"), false,
			"Washington Spirit 2–1 Kansas City Current, 2026 NWSL playoffs",
			"Washington Spirit beat Kansas City Current 2–1 in the 2026 NWSL playoffs on Nov 15 (xG 1.84–0.91)."},
		{"away win", regular, played("1–2", "away"), false,
			"Washington Spirit 1–2 Kansas City Current, 2026 NWSL",
			"Kansas City Current won 2–1 at Washington Spirit in the 2026 NWSL regular season on Nov 15 (xG 0.91–1.84)."},
		{"draw", regular, played("1–1", ""), false,
			"Washington Spirit 1–1 Kansas City Current, 2026 NWSL",
			"Washington Spirit and Kansas City Current drew 1–1 in the 2026 NWSL regular season on Nov 15 (xG 1.84–0.91)."},
		{"shootout", playoffs, played("1–1", "away"), false,
			"Washington Spirit 1–1 Kansas City Current, 2026 NWSL playoffs",
			"Kansas City Current beat Washington Spirit on penalties after a 1–1 draw in the 2026 NWSL playoffs on Nov 15 (xG 0.91–1.84)."},
		{"upcoming", playoffs, fixtureView{HomeTeam: spirit, AwayTeam: current, KickoffUTC: "2026-11-22T20:00:00Z"}, true,
			"Washington Spirit vs Kansas City Current, Nov 22, 2026",
			"Washington Spirit vs Kansas City Current in the 2026 NWSL playoffs on Nov 22: how the teams compare and the match outlook."},
		{"upcoming without a kickoff or outlook", regular, fixtureView{HomeTeam: spirit, AwayTeam: current}, false,
			"Washington Spirit vs Kansas City Current",
			"Washington Spirit vs Kansas City Current in the 2026 NWSL regular season: how the teams compare."},
	} {
		title, description := matchMeta(tc.scope, tc.match, eastern, tc.outlook)
		if title != tc.title {
			t.Errorf("%s: title = %q, want %q", tc.name, title, tc.title)
		}
		if description != tc.description {
			t.Errorf("%s: description = %q, want %q", tc.name, description, tc.description)
		}
	}
}

func TestFixturesMetaFollowsWhatThePageLists(t *testing.T) {
	scope := requestCompetition{Season: "2026", Stage: "Regular Season"}
	for _, tc := range []struct {
		page               seasonPage
		title, description string
	}{
		{seasonPage{HasResults: true, HasUpcomingFixtures: true, HasXG: true}, "2026 NWSL results and fixtures", "Every 2026 NWSL regular-season result and upcoming fixture, with scores and xG."},
		{seasonPage{HasResults: true}, "2026 NWSL results", "Every 2026 NWSL regular-season result, with scores."},
		{seasonPage{HasUpcomingFixtures: true}, "2026 NWSL fixtures", "Every 2026 NWSL regular-season fixture, with kickoff times."},
	} {
		title, description := fixturesMeta(scope, tc.page)
		if title != tc.title || description != tc.description {
			t.Errorf("fixturesMeta(%+v) = %q, %q; want %q, %q", tc.page, title, description, tc.title, tc.description)
		}
	}
	cup := requestCompetition{Season: "2020", Stage: "NWSL Challenge Cup Group Stage", Entry: competition.Entry{Label: "2020 NWSL Challenge Cup Group Stage"}}
	if title, _ := fixturesMeta(cup, seasonPage{HasResults: true}); title != "2020 NWSL Challenge Cup Group Stage results" {
		t.Errorf("Challenge Cup title = %q", title)
	}
}

// Pages that previously shared a title and description now each have their
// own, within the lengths search results show.
func TestPagesHaveTheirOwnTitleAndDescription(t *testing.T) {
	titlePattern := regexp.MustCompile(`<title>([^<]*)</title>`)
	descriptionPattern := regexp.MustCompile(`<meta name="description" content="([^"]*)">`)
	seen := map[string]string{}
	for _, target := range []string{
		"/seasons/2026/regular-season",
		"/seasons/2026/regular-season/fixtures",
		"/seasons/2026/regular-season/schedule-difficulty",
		"/seasons/2026/regular-season/forecast",
		"/seasons/2026/regular-season/clinching",
		"/seasons/2026/regular-season/model-evaluation",
		"/seasons/2026/regular-season/matches/completed",
		"/seasons/2026/regular-season/matches/future-1",
	} {
		response := httptest.NewRecorder()
		NewHandlerWithOptions(fakeStore{season: testSeasonData()}, Options{Rules: testRules(30), ForecastIterations: 20, Location: time.UTC}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s: status = %d", target, response.Code)
		}
		body := response.Body.String()
		title, description := titlePattern.FindStringSubmatch(body), descriptionPattern.FindStringSubmatch(body)
		if title == nil || description == nil {
			t.Fatalf("%s: title = %v, description = %v", target, title, description)
		}
		for _, value := range []string{title[1], description[1]} {
			if other, ok := seen[value]; ok {
				t.Errorf("%s repeats %s's %q", target, other, value)
			}
			seen[value] = target
		}
		if description[1] == siteDescription {
			t.Errorf("%s: uses the site description", target)
		}
		if n := utf8.RuneCountInString(description[1]); n > 155 {
			t.Errorf("%s: description is %d characters: %q", target, n, description[1])
		}
	}
}
