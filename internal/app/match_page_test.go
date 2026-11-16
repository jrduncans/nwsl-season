package app

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

func matchResponse(t *testing.T, data cache.SeasonData, route string) (int, string) {
	t.Helper()
	response := httptest.NewRecorder()
	NewHandlerWithOptions(fakeStore{season: data}, Options{Rules: testRules(30), Location: time.UTC}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, route, nil))
	return response.Code, response.Body.String()
}

func TestResultsRowsLinkTheScoreToTheMatchPage(t *testing.T) {
	_, body := matchResponse(t, testSeasonData(), "/seasons/2026/regular-season/fixtures")
	requireAttr(t, body, ".fixture-result a.fixture-match-link", "href", "matches/completed")
	requireAttr(t, body, ".fixture-result a.fixture-match-link", "href", "matches/future-1")
	requireExact(t, body, `.fixture-match-link[href="matches/completed"]`, "2–1")
	requireAttr(t, body, `.fixture-match-link[href="matches/completed"]`, "aria-label", "2–1: Alpha & Co <script>alert(1)</script> vs Bravo FC, match details")
	requireAttr(t, body, `.fixture-match-link[href="matches/future-1"]`, "aria-label", "vs: Alpha & Co <script>alert(1)</script> vs Bravo FC, match details")
}

func TestUpcomingMatchPageExplainsTheResultsOutlook(t *testing.T) {
	data := testSeasonData()
	code, body := matchResponse(t, data, "/seasons/2026/regular-season/matches/future-1")
	if code != http.StatusOK {
		t.Fatalf("status %d: %s", code, body)
	}
	outlook := fixtureOutlooks(data)["future-1"]
	requireText(t, body, ".match-board", "Match outlook", outlook.HomeWinText, outlook.DrawText, outlook.AwayWinText)
	requireText(t, body, "main h1", "Alpha & Co", "Bravo FC")
	// The match is already chosen, so the season and competition are labels, not controls.
	if selectors := find(t, body, "[data-season-selector], [data-stage-selector]"); len(selectors) != 0 {
		t.Errorf("got %d season/competition selectors on a match page", len(selectors))
	}
	requireText(t, body, ".season-context", "2026", "Regular Season")
	if tables := find(t, body, ".match-factors"); len(tables) != 2 {
		t.Fatalf("got %d factor tables", len(tables))
	}
	requireText(t, body, ".match-board-result", "vs", "Predicted")
	forbidText(t, body, ".match-board-result", "xG")
	requireText(t, body, ".match-factors", "League home rate", "League away rate", "attack", "defense", "= Predicted goals")
	forbidText(t, body, "main", "Expected goals")
	// Equal (here, no) schedule load cancels, so there is no rest row or sentence.
	forbidText(t, body, ".match-factors", "× Rest")
	forbidText(t, body, ".match-meta", "rest")
	requireText(t, body, ".match-ranks", "Season ranks", "Before kickoff, among 2 teams")
	requireExact(t, body, ".match-rank-group th", "Scored", "Allowed", "Differential")
	if xg, goals := find(t, body, ".match-rank-xg"), find(t, body, ".match-rank-goals"); len(xg) != 3 || len(goals) != 3 {
		t.Errorf("got %d xG and %d goals rows, want 3 each", len(xg), len(goals))
	}

	_, body = matchResponse(t, data, "/seasons/2026/regular-season/matches/future-2")
	requireText(t, body, ".match-meta", "on 1 day's rest")
	requireAttr(t, body, ".match-links a", "href", "../../../../explore?opponent=bravo&team=alpha&view=head-to-head")
}

func TestCompletedMatchPageUsesOnlyMatchesBeforeKickoff(t *testing.T) {
	data := testSeasonData()
	data.Games = append(data.Games, cache.Game{
		ASAID: "later", Season: "2026", Stage: "Regular Season", KickoffUTC: "2026-07-05 19:00:00 UTC",
		Status: standings.CompletedStatus, HomeTeamID: "bravo", AwayTeamID: "alpha",
		HomeScore: sql.NullInt64{Int64: 3, Valid: true}, AwayScore: sql.NullInt64{Int64: 0, Valid: true}, Matchday: sql.NullInt64{Int64: 1, Valid: true},
	})
	data.XGoals = []cache.GameXG{{GameID: "completed", Availability: cache.XGAvailable, HomeXG: sql.NullFloat64{Float64: 2.36, Valid: true}, AwayXG: sql.NullFloat64{Float64: 1.11, Valid: true}, Penalties: &cache.PenaltySplit{HomeXG: 0.79}}}

	_, body := matchResponse(t, data, "/seasons/2026/regular-season/matches/completed")
	requireExact(t, body, ".match-board-score", "2–1")
	requireText(t, body, ".match-board", "Before kickoff")
	requireExact(t, body, ".match-board .stat-line dt", "xG", "npxG", "Predicted")
	requireExact(t, body, ".match-board .stat-home", "2.36", "1.57")
	requireExact(t, body, ".match-board .stat-away", "1.11", "1.11")
	requireText(t, body, ".match-factors", "No matches yet")
	forbidElements(t, body, ".match-ranks")

	_, body = matchResponse(t, data, "/seasons/2026/regular-season/matches/later")
	// The earlier result counts; the target and anything after it do not.
	requireText(t, body, ".match-ranks", "+1.00", "-1.00", "2.36", "1.11")
	// The better rank on each row is marked, and only one side.
	if better := find(t, body, ".match-rank-better"); len(better) != 6 {
		t.Errorf("got %d better-rank cells, want 6", len(better))
	}
	forbidText(t, body, ".match-ranks .note", "withheld")

	data.XGoals = nil
	_, body = matchResponse(t, data, "/seasons/2026/regular-season/matches/later")
	requireText(t, body, ".match-ranks .note", "xG ranks are withheld")
	requireText(t, body, ".match-factors", "No xG yet")
}

func TestMatchTrainingGamesUnplayTheTargetAndLaterResults(t *testing.T) {
	kickoff := time.Date(2026, 7, 1, 19, 0, 0, 0, time.UTC)
	score := 1
	games := []standings.Game{
		{ID: "before", Status: standings.CompletedStatus, Kickoff: kickoff.Add(-time.Hour), HomeScore: &score, AwayScore: &score},
		{ID: "target", Status: standings.CompletedStatus, Kickoff: kickoff, HomeScore: &score, AwayScore: &score},
		{ID: "same", Status: standings.CompletedStatus, Kickoff: kickoff, HomeScore: &score, AwayScore: &score},
		{ID: "after", Status: standings.CompletedStatus, Kickoff: kickoff.Add(time.Hour), HomeScore: &score, AwayScore: &score},
	}
	got := matchTrainingGames(games, "target")
	for i, want := range []string{standings.CompletedStatus, fixtures.PreMatchStatus, fixtures.PreMatchStatus, fixtures.PreMatchStatus} {
		if got[i].Status != want || (want == fixtures.PreMatchStatus) != (got[i].HomeScore == nil) {
			t.Errorf("%s = %+v, want %s", got[i].ID, got[i], want)
		}
	}
	if games[1].Status != standings.CompletedStatus {
		t.Fatal("input mutated")
	}
	games[1].Status = fixtures.PreMatchStatus
	got = matchTrainingGames(games, "target")
	if got[3].Status != standings.CompletedStatus {
		t.Fatal("an upcoming target must keep every completed result, as Results does")
	}
}

func TestMatchPagesExistOnlyForKnownRegularSeasonAndPlayoffMatches(t *testing.T) {
	for _, route := range []string{
		"/seasons/2026/regular-season/matches/unknown",
		"/seasons/2026/challenge-cup-final/matches/completed",
	} {
		if code, _ := matchResponse(t, testSeasonData(), route); code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", route, code)
		}
	}
}

func TestPlayoffFinalMatchPageIsNeutralAndMatchesTheBracket(t *testing.T) {
	regular := resolvedHandoffSeason()
	seeds := playoffSeedsFrom("2026", regular)
	games := []cache.Game{
		forecastPlayoffGame(seeds, "qf1", 1, 8, true), forecastPlayoffGame(seeds, "qf2", 4, 5, true),
		forecastPlayoffGame(seeds, "qf3", 2, 7, true), forecastPlayoffGame(seeds, "qf4", 3, 6, true),
		forecastPlayoffGame(seeds, "sf1", 1, 4, true), forecastPlayoffGame(seeds, "sf2", 2, 3, true),
		forecastPlayoffGame(seeds, "final", 2, 1, false),
	}
	for i := range games {
		games[i].KickoffUTC = time.Date(2026, 11, 1+i, 20, 0, 0, 0, time.UTC).Format(time.RFC3339)
	}
	store := handoffStore{regular: regular, playoffs: cache.SeasonData{Teams: regular.Teams, Games: games}}
	odds := (&application{store: store}).playoffOutlooks(context.Background(), "2026", store.playoffs)["final"]
	if odds.HomeAdvanceText == "" {
		t.Fatal("fixture lacks a bracket outlook")
	}

	bracketBody := handoffResponse(t, store, "/seasons/2026/playoffs")
	requireAttr(t, bracketBody, ".bracket-match-link", "href", "playoffs/matches/final")
	requireAttr(t, bracketBody, ".bracket-match-link", "href", "playoffs/matches/qf1")

	body := handoffResponse(t, store, "/seasons/2026/playoffs/matches/final")
	requireText(t, body, ".match-meta", "Final", "Neutral venue")
	requireText(t, body, ".match-board", "Advance "+odds.HomeAdvanceText, "Advance "+odds.AwayAdvanceText)
	requireText(t, body, ".match-board", "Extra time "+odds.DrawText)
	requireText(t, body, ".match-factors", "League neutral rate")
	forbidText(t, body, ".match-factors", "× Rest", "League home rate")
	requireText(t, body, ".match-ranks", "Season ranks", "Regular season, among")
	if !strings.Contains(text(find(t, body, "main")[0]), "No schedule load in the playoffs") {
		t.Error("playoff note missing")
	}
}

func TestRelativeURLNeverEndsAtAnAncestorDirectory(t *testing.T) {
	for _, c := range []struct{ from, target, want string }{
		{"/seasons/2026/regular-season/fixtures", "/seasons/2026/regular-season", "../regular-season"},
		{"/seasons/2026/playoffs/matches/abc", "/seasons/2026/playoffs", "../../playoffs"},
		{"/seasons/2026/playoffs/matches/abc", "/seasons/2026/playoffs/forecast", "../forecast"},
		{"/seasons/2026/playoffs/matches/abc", "/explore", "../../../../explore"},
		{"/seasons/2026/playoffs/matches/abc", "/", "../../../.."},
		{"/seasons/2026/playoffs", "/seasons/2026/playoffs/matches", "playoffs/matches"},
	} {
		if got := relativeURL(c.from, c.target); got != c.want {
			t.Errorf("relativeURL(%q, %q) = %q, want %q", c.from, c.target, got, c.want)
		}
	}
}
