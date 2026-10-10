//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	playwright "github.com/mxschmitt/playwright-go"

	"github.com/jrduncans/nwsl-season/internal/asa"
	"github.com/jrduncans/nwsl-season/internal/asatest"
	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/clinching"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/config"
	"github.com/jrduncans/nwsl-season/internal/qualification"
	"github.com/jrduncans/nwsl-season/internal/scenariorefresh"
	"github.com/jrduncans/nwsl-season/internal/server"
	"github.com/jrduncans/nwsl-season/internal/standings"
	"github.com/jrduncans/nwsl-season/internal/syncer"
)

// The journeys in this file change what the fake ASA serves, run the
// scheduler's check, and then read pages in the browser. They encode
// architecture invariants: pages read the cache only, an incomplete fixture
// inventory never replaces a complete one, and a failing source leaves the last
// good data in place.
//
// 2026 Regular Season rules need a full inventory (16 teams, 240 games), so
// every journey uses the 16-team double round robin rather than a smaller
// season. Each journey starts from its own fixture and its own planner clock,
// which the test advances instead of sleeping.

// journeyClock is the scheduler's planning clock. It is safe for the
// scheduler goroutine to read while the test advances it.
type journeyClock struct {
	nanos    atomic.Int64
	mu       sync.Mutex
	nextRead func()
}

func newJourneyClock(start time.Time) *journeyClock {
	c := &journeyClock{}
	c.nanos.Store(start.UnixNano())
	return c
}

func (c *journeyClock) Now() time.Time {
	now := time.Unix(0, c.nanos.Load()).UTC()
	c.mu.Lock()
	hook := c.nextRead
	c.nextRead = nil
	c.mu.Unlock()
	if hook != nil {
		hook()
	}
	return now
}

// holdNextRead lets a test change source data while the next planner holds
// the old clock value. No sleep or count of scheduler clock calls is needed.
func (c *journeyClock) holdNextRead(t *testing.T) (<-chan struct{}, func()) {
	t.Helper()
	entered, release := make(chan struct{}), make(chan struct{})
	resume := sync.OnceFunc(func() { close(release) })
	t.Cleanup(resume)
	c.mu.Lock()
	c.nextRead = func() {
		close(entered)
		<-release
	}
	c.mu.Unlock()
	return entered, resume
}

func (c *journeyClock) Advance(d time.Duration) { c.nanos.Add(int64(d)) }

// journeyConfig describes a journey's starting season.
type journeyConfig struct {
	// playedRounds is the number of leading rounds that are completed. The
	// remaining rounds are PreMatch.
	playedRounds int
	// firstPendingIn is when the first unplayed round kicks off, relative to the
	// planner clock's start. The round's games kick off over the following
	// seven hours. A game is polled for its result once the clock passes its
	// kickoff plus the two-hour completion grace, so a journey that wants the
	// first sync to leave these games unpolled, and the next check to poll them
	// for the first time, sets this to an hour and advances the clock by half a
	// day.
	firstPendingIn time.Duration
	// leaveUnplayed, when set, completes every game of the first pending round
	// for which it returns false, so only those games stay unplayed.
	leaveUnplayed func(game asa.Game) bool
	// score gives completed games' results; nil uses halfSeasonScore.
	score func(game asa.Game) (home, away int)
	// xg is the share of completed games that have xG, 0 to 1.
	xg float64
	// startScheduler makes the server start its scheduler (see journey.Server.Start).
	startScheduler bool
	// checkInterval, when set, is the scheduler's check interval.
	checkInterval time.Duration
}

// halfSeasonScore gives deterministic wins, draws and losses.
func halfSeasonScore(game asa.Game) (int, int) {
	switch game.GameID[len(game.GameID)-1] % 3 {
	case 0:
		return 2, 1
	case 1:
		return 1, 1
	default:
		return 0, 1
	}
}

// journey is a running app whose scenario, ASA data and planner clock the test
// controls.
type journey struct {
	*fixture
	// Games is the test's copy of the fake's games. Change it with setGames or
	// upsert so the fake and the copy stay equal.
	Games []asa.Game
	Teams []asa.Team
	cfg   config.Config
	Clock *journeyClock
}

// newJourney builds the season, serves it, and fills the cache.
func newJourney(t *testing.T, cfg journeyConfig) *journey {
	t.Helper()
	appConfig := testConfig(t)
	if cfg.checkInterval != 0 {
		appConfig.SyncCheckInterval = cfg.checkInterval
	}

	// The planner clock starts three days in the past and is only ever moved
	// forward by less than that. The syncer stamps the end of every source
	// operation with the wall clock and rejects an operation whose planner
	// start is later than that, so the planner clock must never pass real
	// time. The scheduler reads the clock only to decide what is due, so being
	// in the past is harmless.
	clock := newJourneyClock(time.Now().UTC().Add(-72 * time.Hour).Truncate(time.Minute))
	pending := clock.Now().Add(cfg.firstPendingIn)
	start := pending.AddDate(0, 0, -7*cfg.playedRounds)
	scenario := asatest.Season(fixtureTeams, start)
	for i := range scenario.Games {
		// Season names the games after start's year, which may differ.
		scenario.Games[i].SeasonName = currentSeason
	}
	score := cfg.score
	if score == nil {
		score = halfSeasonScore
	}
	// Round r kicks off at start + 7r days, so this plays rounds before
	// playedRounds and leaves the round that kicks off at pending.
	scenario.PlayThrough(pending, score)
	if cfg.leaveUnplayed != nil {
		// Complete the rest of the first pending round, so only a few games
		// remain and the scheduler's scenario search stays small.
		roundEnd := pending.Add(24 * time.Hour)
		for i, game := range scenario.Games {
			kickoff, err := time.Parse("2006-01-02 15:04:05 MST", game.DateTimeUTC)
			if err != nil {
				t.Fatalf("parse kickoff %q: %v", game.DateTimeUTC, err)
			}
			if game.Status == "PreMatch" && kickoff.Before(roundEnd) && !cfg.leaveUnplayed(game) {
				home, away := score(game)
				scenario.Games[i] = withResult(game, home, away)
			}
		}
	}
	scenario.WithXG(cfg.xg)
	fake := asatest.New(t)
	fake.Load(scenario)

	srv, err := server.Build(context.Background(), appConfig, server.Options{
		Logger:             slog.New(slog.NewTextHandler(io.Discard, nil)),
		ASABaseURL:         fake.URL(),
		StartScheduler:     cfg.startScheduler,
		Now:                clock.Now,
		ForecastIterations: e2eForecastIterations,
	})
	if err != nil {
		t.Fatalf("server.Build: %v", err)
	}
	t.Cleanup(func() {
		srv.Stop()
		srv.Wait()
		if err := srv.Close(); err != nil {
			t.Errorf("close server: %v", err)
		}
	})
	fillCache(t, srv, fake)

	mux := http.NewServeMux()
	mux.Handle(mountPrefix+"/", http.StripPrefix(mountPrefix, srv.Handler()))
	web := httptest.NewServer(mux)
	t.Cleanup(web.Close)

	return &journey{
		fixture: &fixture{ASA: fake, Server: srv, BaseURL: web.URL + mountPrefix + "/"},
		Games:   scenario.Games,
		Teams:   scenario.Teams,
		Clock:   clock,
		cfg:     appConfig,
	}
}

// sync runs scheduler checks until one makes no ASA request, so every due job
// has run.
func (j *journey) sync(t *testing.T) {
	t.Helper()
	fillCache(t, j.Server, j.ASA)
}

// setGames replaces the fake's games and the test's copy.
func (j *journey) setGames(games []asa.Game) {
	j.Games = games
	j.ASA.SetGames(games)
}

// upsert replaces the game with the same ID in the fake and the test's copy.
func (j *journey) upsert(game asa.Game) {
	games := append([]asa.Game(nil), j.Games...)
	for i := range games {
		if games[i].GameID == game.GameID {
			games[i] = game
		}
	}
	j.Games = games
	j.ASA.UpsertGame(game)
}

// pendingGameOf returns the team's only unplayed game. The late-season
// journeys leave exactly one per team.
func (j *journey) pendingGameOf(t *testing.T, teamID string) asa.Game {
	t.Helper()
	var found []asa.Game
	for _, game := range j.Games {
		if game.Status == "PreMatch" && (game.HomeTeamID == teamID || game.AwayTeamID == teamID) {
			found = append(found, game)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%s has %d unplayed games, want 1", teamID, len(found))
	}
	return found[0]
}

// withResult returns game completed with the score. LastUpdatedUTC follows the
// asatest convention for completed games.
func withResult(game asa.Game, home, away int) asa.Game {
	one := &asatest.Scenario{Games: []asa.Game{game}}
	farFuture := time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
	return one.PlayThrough(farFuture, func(asa.Game) (int, int) { return home, away }).Games[0]
}

// winFor returns game completed 1-0 for the team.
func winFor(game asa.Game, teamID string) asa.Game {
	if game.HomeTeamID == teamID {
		return withResult(game, 1, 0)
	}
	return withResult(game, 0, 1)
}

// tableRow is what the standings page shows for one team.
type tableRow struct {
	ID         string `json:"id"`
	Played     int    `json:"played"`
	Wins       int    `json:"wins"`
	Draws      int    `json:"draws"`
	Losses     int    `json:"losses"`
	GoalsFor   int    `json:"gf"`
	GoalsOver  int    `json:"ga"`
	Points     int    `json:"points"`
	Badge      string `json:"badge"`
	Eliminated bool   `json:"eliminated"`
}

// readStandings returns the standings table's rows in page order. Points and
// goals come from the totals the page stores whichever display mode is active.
func readStandings(t *testing.T, page playwright.Page) []tableRow {
	t.Helper()
	raw, err := page.Evaluate(`() => JSON.stringify(Array.from(document.querySelectorAll('table.standings tbody tr')).map((tr) => {
		const cells = tr.querySelectorAll('td');
		const count = (i) => Number(cells[i].textContent.trim());
		const badge = tr.querySelector('.qualification-badge');
		const goals = tr.querySelector('[data-standings-stat-column="goals"]').dataset.total.split('/').map(Number);
		return {
			id: tr.dataset.teamId,
			played: count(1), wins: count(2), draws: count(3), losses: count(4),
			gf: goals[0], ga: goals[1],
			points: Number(tr.querySelector('[data-standings-points]').dataset.total),
			badge: badge ? badge.textContent.trim() : '',
			eliminated: tr.querySelector('.elimination-status') !== null,
		};
	}))`)
	if err != nil {
		t.Fatalf("read standings: %v", err)
	}
	text, ok := raw.(string)
	if !ok {
		t.Fatalf("read standings: got %T, want a JSON string", raw)
	}
	var rows []tableRow
	if err := json.Unmarshal([]byte(text), &rows); err != nil {
		t.Fatalf("decode standings %q: %v", text, err)
	}
	return rows
}

// expectedTable computes each team's record from the completed games the fake
// serves, independently of the app's standings code.
func expectedTable(teams []asa.Team, games []asa.Game) map[string]tableRow {
	table := map[string]tableRow{}
	for _, team := range teams {
		table[team.TeamID] = tableRow{ID: team.TeamID}
	}
	apply := func(id string, scored, conceded int) {
		row := table[id]
		row.Played++
		row.GoalsFor += scored
		row.GoalsOver += conceded
		switch {
		case scored > conceded:
			row.Wins++
			row.Points += 3
		case scored == conceded:
			row.Draws++
			row.Points++
		default:
			row.Losses++
		}
		table[id] = row
	}
	for _, game := range games {
		if game.Status != "FullTime" || game.HomeScore == nil || game.AwayScore == nil {
			continue
		}
		apply(game.HomeTeamID, *game.HomeScore, *game.AwayScore)
		apply(game.AwayTeamID, *game.AwayScore, *game.HomeScore)
	}
	return table
}

// assertRecords fails unless the page rows carry exactly the expected records.
// The badge columns are not compared.
func assertRecords(t *testing.T, got []tableRow, want map[string]tableRow) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("standings has %d rows, want %d", len(got), len(want))
	}
	for _, row := range got {
		expected, ok := want[row.ID]
		if !ok {
			t.Errorf("standings has unexpected team %q", row.ID)
			continue
		}
		row.Badge, row.Eliminated = "", false
		if row != expected {
			t.Errorf("%s: standings show %+v, want %+v", row.ID, row, expected)
		}
	}
}

// rowOf returns the row for a team.
func rowOf(t *testing.T, rows []tableRow, teamID string) tableRow {
	t.Helper()
	for _, row := range rows {
		if row.ID == teamID {
			return row
		}
	}
	t.Fatalf("standings have no row for %s", teamID)
	return tableRow{}
}

// fixtureCount returns how many fixtures the season's fixtures page lists.
func fixtureCount(t *testing.T, page playwright.Page) int {
	t.Helper()
	count, err := page.Locator("[data-fixture-home-team]").Count()
	if err != nil {
		t.Fatalf("count fixtures: %v", err)
	}
	return count
}

// clinchedLine is the accessible name the clinching page gives a team already
// guaranteed a playoff place.
func clinchedLine(teamName string) string {
	return teamName + " has already clinched the playoffs"
}

// assertOthersClinched fails unless the standings still show a playoffs
// indicator for the strongest team, which has clinched in every late-season
// state. It makes "team-0 has no indicator" a meaningful check: indicators are
// rendered, just not for team-0.
func assertOthersClinched(t *testing.T, rows []tableRow) {
	t.Helper()
	if row := rowOf(t, rows, "team-1"); row.Badge == "" {
		t.Errorf("team-1 should still show a clinched indicator, got none")
	}
}

// clinchedOnPage reports whether the clinching page lists the team as already
// clinched.
func clinchedOnPage(t *testing.T, page playwright.Page, teamName string) bool {
	t.Helper()
	count, err := page.Locator(fmt.Sprintf("li[aria-label=%q]", clinchedLine(teamName))).Count()
	if err != nil {
		t.Fatalf("count clinched entries: %v", err)
	}
	return count > 0
}

// httpBody returns the body of a GET to an app path.
func httpBody(t *testing.T, f *fixture, path string) string {
	t.Helper()
	resp, err := http.Get(f.URL(path))
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d, read error %v", path, resp.StatusCode, err)
	}
	return string(body)
}

// eventually polls until check returns true or the timeout passes. It waits on
// a ticker rather than sleeping, so it returns as soon as the condition holds.
func eventually(t *testing.T, timeout time.Duration, what string, check func() bool) {
	t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if check() {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("timed out after %s waiting for %s", timeout, what)
		case <-ticker.C:
		}
	}
}

// playoffs returns the current rules' playoff achievement.
func playoffs(t *testing.T) competition.Achievement {
	t.Helper()
	rules, ok := competition.ForSeason(currentSeason, "Regular Season")
	if !ok {
		t.Fatal("no competition rules for the current season")
	}
	for _, achievement := range rules.Achievements {
		if achievement.ID == competition.AchievementPlayoffs {
			return achievement
		}
	}
	t.Fatal("rules have no playoffs achievement")
	return competition.Achievement{}
}

// evaluatePlayoffs asks the clinching evaluator, on the given fixture set,
// whether the team has clinched a playoff place.
func evaluatePlayoffs(t *testing.T, teams []asa.Team, games []asa.Game, teamID string) clinching.AchievementResult {
	t.Helper()
	var stTeams []standings.Team
	for _, team := range teams {
		stTeams = append(stTeams, standings.Team{ID: team.TeamID, Name: team.TeamName, ShortName: team.TeamShortName, Abbreviation: team.TeamAbbreviation})
	}
	var stGames []standings.Game
	var pending []asa.Game
	for _, game := range games {
		stGame := standings.Game{ID: game.GameID, Status: game.Status, HomeTeamID: game.HomeTeamID, AwayTeamID: game.AwayTeamID, HomeScore: game.HomeScore, AwayScore: game.AwayScore}
		if game.Status == "FullTime" {
			stGame.Status = standings.CompletedStatus
		} else {
			pending = append(pending, game)
		}
		stGames = append(stGames, stGame)
	}
	sort.Slice(pending, func(a, b int) bool {
		if pending[a].DateTimeUTC != pending[b].DateTimeUTC {
			return pending[a].DateTimeUTC < pending[b].DateTimeUTC
		}
		return pending[a].GameID < pending[b].GameID
	})
	var order []string
	for _, game := range pending {
		order = append(order, game.GameID)
	}
	evaluator, err := clinching.NewEvaluator(stTeams, stGames, order)
	if err != nil {
		t.Fatalf("clinching.NewEvaluator: %v", err)
	}
	result, err := evaluator.EvaluateStatus(context.Background(), teamID, playoffs(t), nil)
	if err != nil {
		t.Fatalf("evaluate %s: %v", teamID, err)
	}
	return result
}

// Late-season arrangement shared by J3, J4, J6 and J7.
//
// The league has played 29 of 30 rounds. Strength is a fixed order, strongest
// first: team-1 ... team-7, then team-0, then team-8 ... team-15. The stronger
// team wins 1-0 in every completed game, with one exception: both games between
// team-1 and team-8 are 1-1 draws.
//
// Only two games are unplayed, both in the last round: team-0 v team-14 and
// team-8 v team-5. Every other game has a result. Team-14 and team-5 are not
// near the playoff line.
//
// Points after 29 rounds:
//
//	team-0 (8th strongest): wins both games against each of the 8 weaker teams
//	  = 16 wins, less the unplayed win at team-14, so 15 wins = 45 points.
//	team-8 (9th strongest): wins both games against each of the 7 weaker teams
//	  = 14 wins = 42 points, plus the two draws with team-1 = 44 points. Its
//	  unplayed game is against the stronger team-5, so it is not missing a win.
//	team-9 and weaker: team-9 wins both games against each of the 6 weaker
//	  teams = 12 wins = 36 points, and it has no unplayed game, so 36 is its
//	  final total. Weaker teams have fewer wins.
//	team-1 ... team-7: at least 54 points.
//
// Playoffs are the top 8 of 16, so team-0 has clinched when fewer than 8 of
// its 15 opponents can finish with at least as many points as it is guaranteed
// (its worst case, losing every unplayed game).
//
// Before the matchday, team-0's worst case is 45 points (it loses at team-14).
// Team-8 can reach 44 + 3 = 47 > 45, and so can the seven stronger teams, so 8
// opponents can finish strictly ahead: team-0 has not clinched.
//
// If team-0 beats team-14, the worst case is 45 + 3 = 48 points. Team-8 can
// reach only 47 < 48. Team-9 and weaker are final at 36 or fewer points: their
// games are all played, and team-14's only unplayed game is the one team-0 just
// won. So only the seven stronger teams can reach 48: fewer than 8, so team-0 has clinched the playoffs.
//
// If team-8's last game were missing from the schedule, team-8 could not gain
// those 3 points: it would stay at 44 < 45 and team-0 would appear clinched
// even though the result has not happened. J4 uses that to show the app does
// not publish such an indicator from an incomplete inventory.
var strengthOrder = []string{
	"team-1", "team-2", "team-3", "team-4", "team-5", "team-6", "team-7",
	"team-0", "team-8", "team-9", "team-10", "team-11", "team-12", "team-13", "team-14", "team-15",
}

func strength(teamID string) int {
	for i, id := range strengthOrder {
		if id == teamID {
			return i
		}
	}
	panic("unknown team " + teamID)
}

// lateSeasonScore is the strength order's result, with the team-1 v team-8
// draws.
func lateSeasonScore(game asa.Game) (int, int) {
	pair := game.HomeTeamID + " " + game.AwayTeamID
	if pair == "team-1 team-8" || pair == "team-8 team-1" {
		return 1, 1
	}
	if strength(game.HomeTeamID) < strength(game.AwayTeamID) {
		return 1, 0
	}
	return 0, 1
}

const (
	teamZero   = "team-0"
	teamZeroNm = "Team 0 FC"
	teamEight  = "team-8"
	// lateRounds is the number of completed rounds in the late-season journeys.
	lateRounds = 29
	// lateRoundsDue moves the clock past the last round's final kickoff (7h
	// after the first) plus the completion grace.
	lateRoundsDue = 12 * time.Hour
)

// lateSeason is the journeyConfig for the arrangement above. The last round is
// not yet due at the first sync, so the scheduler has not polled its games.
// After lateRoundsDue the games are due and the next check polls them for the
// first time.
func lateSeason() journeyConfig {
	return journeyConfig{
		playedRounds:   lateRounds,
		firstPendingIn: time.Hour,
		leaveUnplayed: func(game asa.Game) bool {
			return game.HomeTeamID == teamZero || game.AwayTeamID == teamZero ||
				game.HomeTeamID == teamEight || game.AwayTeamID == teamEight
		},
		score: lateSeasonScore,
		xg:    1,
	}
}

// assertLateSeasonArithmetic checks the numbers in the late-season comment
// against the games the fake serves, and against the clinching evaluator.
func assertLateSeasonArithmetic(t *testing.T, j *journey) {
	t.Helper()
	table := expectedTable(j.Teams, j.Games)
	if got := table[teamZero].Points; got != 45 {
		t.Fatalf("team-0 has %d points, want 45", got)
	}
	if got := table[teamEight].Points; got != 44 {
		t.Fatalf("team-8 has %d points, want 44", got)
	}
	for _, team := range j.Teams {
		if team.TeamID == teamZero || team.TeamID == teamEight {
			continue
		}
		points := table[team.TeamID].Points
		rank := strength(team.TeamID)
		if rank < 7 && points < 54 {
			t.Fatalf("%s has %d points, want at least 54", team.TeamID, points)
		}
		if rank > 8 && points > 36 {
			t.Fatalf("%s has %d points, want at most 36", team.TeamID, points)
		}
	}
	if got := evaluatePlayoffs(t, j.Teams, j.Games, teamZero).Status; got != clinching.NotClinched {
		t.Fatalf("before the matchday, team-0 playoffs status = %q, want %q", got, clinching.NotClinched)
	}
	won := winFor(j.pendingGameOf(t, teamZero), teamZero)
	after := append([]asa.Game(nil), j.Games...)
	for i := range after {
		if after[i].GameID == won.GameID {
			after[i] = won
		}
	}
	if got := evaluatePlayoffs(t, j.Teams, after, teamZero).Status; got != clinching.Clinched {
		t.Fatalf("after team-0 wins, its playoffs status = %q, want %q", got, clinching.Clinched)
	}
}

// checkTeamZero visits the standings and clinching pages at each viewport and
// checks team-0's playoff indicator against wantClinched. It returns the
// standings rows from the desktop viewport.
func checkTeamZero(t *testing.T, j *journey, wantClinched bool, viewports ...viewport) []tableRow {
	t.Helper()
	var desktop []tableRow
	for _, vp := range viewports {
		t.Run(vp.Name, func(t *testing.T) {
			page := newPage(t, vp)
			visit(t, page, j.URL(""))
			rows := readStandings(t, page)
			if vp == Desktop {
				desktop = rows
			}
			row := rowOf(t, rows, teamZero)
			if got := strings.Contains(row.Badge, "Playoffs"); got != wantClinched {
				t.Errorf("standings badge for team-0 = %q, clinched indicator want %v", row.Badge, wantClinched)
			}
			assertNoHorizontalOverflow(t, page)

			visit(t, page, j.URL("seasons/"+currentSeason+"/clinching"))
			if got := clinchedOnPage(t, page, teamZeroNm); got != wantClinched {
				t.Errorf("clinching page lists team-0 as clinched = %v, want %v", got, wantClinched)
			}
			assertNoHorizontalOverflow(t, page)
		})
	}
	return desktop
}

// TestJ3Matchday is journey J3: a result added to the fake after the first sync
// changes the standings, and the team the arrangement says it clinches shows
// the clinched indicator on the standings and clinching pages.
func TestJ3Matchday(t *testing.T) {
	t.Parallel()
	j := newJourney(t, lateSeason())
	assertLateSeasonArithmetic(t, j)
	before := expectedTable(j.Teams, j.Games)

	var beforeRows []tableRow
	t.Run("before the result", func(t *testing.T) {
		beforeRows = checkTeamZero(t, j, false, Desktop)
		assertRecords(t, beforeRows, before)
	})

	// Team-0 wins its last game at team-14.
	j.upsert(winFor(j.pendingGameOf(t, teamZero), teamZero))
	j.Clock.Advance(lateRoundsDue)
	j.sync(t)

	after := expectedTable(j.Teams, j.Games)
	if after[teamZero].Points != before[teamZero].Points+3 {
		t.Fatalf("test bug: the result should add 3 points, %d -> %d", before[teamZero].Points, after[teamZero].Points)
	}
	t.Run("after the result", func(t *testing.T) {
		// Mobile too: the clinched badges are the widest standings rows.
		rows := checkTeamZero(t, j, true, Desktop, Mobile)
		assertRecords(t, rows, after)
		if got := rowOf(t, rows, teamZero).Points; got == rowOf(t, beforeRows, teamZero).Points {
			t.Errorf("team-0 points did not change from %d", got)
		}
	})
}

// requestsFor returns the fake's requests for a path since its last reset.
func requestsFor(fake *asatest.Server, path string) []asatest.Request {
	var out []asatest.Request
	for _, request := range fake.Requests() {
		if request.Path == path {
			out = append(out, request)
		}
	}
	return out
}

// runMaintenanceSync runs the selected-scope sync that cmd/sync runs, against
// the server's cache and the fake ASA, and returns its result.
func (j *journey) runMaintenanceSync(t *testing.T) (cache.SyncRun, error) {
	t.Helper()
	db, err := cache.Open(context.Background(), j.cfg.DBPath)
	if err != nil {
		t.Fatalf("open cache for maintenance sync: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("close maintenance cache handle: %v", err)
		}
	}()
	rules, ok := competition.ForSeason(currentSeason, "Regular Season")
	if !ok {
		t.Fatal("no competition rules for the current season")
	}
	service := syncer.Service{
		ASA:                  j.ASA.Client(),
		Store:                db,
		QualificationTimeout: j.cfg.QualificationBudget,
		ScenarioTimeout:      j.cfg.ScenarioBudget,
		HistoryRetention:     j.cfg.HistoryRetention,
		Qualification:        qualification.Refresher{Store: db, Rules: rules, Budget: j.cfg.QualificationBudget},
		Scenarios:            scenariorefresh.Refresher{Store: db, Rules: rules, Budget: j.cfg.ScenarioBudget},
	}
	ctx, cancel := context.WithTimeout(context.Background(), j.cfg.SyncTimeout)
	defer cancel()
	return service.Run(ctx, syncer.RunOptions{
		Season:        currentSeason,
		Stage:         rules.Stage,
		ExpectedTeams: rules.ExpectedTeams,
		GamesPerTeam:  rules.GamesPerTeam,
		Trigger:       "cli",
	})
}

// TestJ4IncompleteInventory is journey J4: after a complete sync, a fixture
// disappears from /nwsl/games. A full-inventory sync sees 239 fixtures, so the
// earlier 240-fixture list is kept, and no clinch indicator is published from
// the incomplete data.
func TestJ4IncompleteInventory(t *testing.T) {
	t.Parallel()
	j := newJourney(t, lateSeason())
	assertLateSeasonArithmetic(t, j)
	before := expectedTable(j.Teams, j.Games)

	// Without team-8's last game, team-8 would stay at 44 points and the
	// evaluator would call team-0 clinched. The app must not publish that.
	missing := j.pendingGameOf(t, teamEight)
	var incomplete []asa.Game
	for _, game := range j.Games {
		if game.GameID != missing.GameID {
			incomplete = append(incomplete, game)
		}
	}
	if got := evaluatePlayoffs(t, j.Teams, incomplete, teamZero).Status; got != clinching.Clinched {
		t.Fatalf("test bug: without team-8's last game team-0 should look clinched, got %q", got)
	}

	// First let the scheduler poll the games that are now due. The vanished
	// fixture is among them; an ID missing from a targeted response is only
	// an unchanged observation, so the cache keeps it.
	j.ASA.ResetRequests()
	j.setGames(incomplete)
	j.Clock.Advance(lateRoundsDue)
	j.sync(t)
	if len(requestsFor(j.ASA, asatest.PathGames)) == 0 {
		t.Fatal("the scheduler never polled the due games")
	}

	// The scheduler's next full-inventory audit is a week after the first sync,
	// stamped with the wall clock, so a test cannot make it due. The operator's
	// sync command (cmd/sync) fetches the same full inventory through the same
	// cache and sync lease, so the journey runs that instead.
	j.ASA.ResetRequests()
	run, err := j.runMaintenanceSync(t)
	if err == nil {
		t.Fatalf("the sync accepted an incomplete inventory: %+v", run)
	}
	if !strings.Contains(err.Error(), "inventory has 239 games, want 240") {
		t.Fatalf("the sync failed for another reason than the incomplete inventory: %v", err)
	}

	var audited bool
	for _, request := range requestsFor(j.ASA, asatest.PathGames) {
		t.Logf("games request: %v", request.Query)
		if request.Query.Get("game_id") == "" {
			audited = true
		}
	}
	if !audited {
		t.Fatalf("the maintenance sync never requested the full games inventory: %v", j.ASA.Requests())
	}

	// Desktop only: the invariant is the data, and J1 checks every page on a phone.
	for _, vp := range []viewport{Desktop} {
		t.Run(vp.Name, func(t *testing.T) {
			page := newPage(t, vp)
			visit(t, page, j.URL("seasons/"+currentSeason+"/fixtures"))
			if got := fixtureCount(t, page); got != fixtureGames {
				t.Errorf("fixtures page lists %d fixtures, want the earlier %d", got, fixtureGames)
			}
			assertNoHorizontalOverflow(t, page)

			visit(t, page, j.URL(""))
			rows := readStandings(t, page)
			assertRecords(t, rows, before)
			if row := rowOf(t, rows, teamZero); strings.Contains(row.Badge, "Playoffs") {
				t.Errorf("standings published a playoffs indicator for team-0 from incomplete data: %+v", row)
			}
			assertOthersClinched(t, rows)

			visit(t, page, j.URL("seasons/"+currentSeason+"/clinching"))
			if clinchedOnPage(t, page, teamZeroNm) {
				t.Error("clinching page lists team-0 as clinched from incomplete data")
			}
			if !clinchedOnPage(t, page, "Team 1 FC") {
				t.Error("clinching page lost the earlier clinched entries")
			}
		})
	}
}

// cacheStatus is the part of /cache/status that J6 reads.
type cacheStatus struct {
	OK          bool       `json:"ok"`
	LastAttempt *statusRun `json:"last_attempt"`
	LastSuccess *statusRun `json:"last_success"`
}

type statusRun struct {
	ID           int64  `json:"id"`
	FinishedAt   string `json:"finished_at"`
	Outcome      string `json:"outcome"`
	ErrorSummary string `json:"error_summary"`
}

func readCacheStatus(t *testing.T, f *fixture) cacheStatus {
	t.Helper()
	body := httpBody(t, f, "cache/status")
	var status cacheStatus
	if err := json.Unmarshal([]byte(body), &status); err != nil {
		t.Fatalf("decode /cache/status %q: %v", body, err)
	}
	return status
}

// TestJ6ASAErrors is journey J6: ASA answers 503 to a later games request.
// Pages keep the last good data and the next check recovers. Failure reporting
// through /cache/status remains an open follow-up in docs/test-strategy-plan.md.
func TestJ6ASAErrors(t *testing.T) {
	t.Parallel()
	j := newJourney(t, lateSeason())
	before := expectedTable(j.Teams, j.Games)
	good := readCacheStatus(t, j.fixture)

	// Team-0's result is waiting at ASA, but every attempt to fetch it fails.
	// The client tries three times (the first request and two retries), so
	// three failures fail exactly one operation.
	j.upsert(winFor(j.pendingGameOf(t, teamZero), teamZero))
	j.Clock.Advance(lateRoundsDue)
	j.ASA.ResetRequests()
	j.ASA.FailNext(asatest.PathGames, http.StatusServiceUnavailable, 3)
	if err := j.Server.CheckNow(context.Background()); err == nil {
		t.Fatal("CheckNow succeeded although ASA answered 503")
	}
	if got := len(requestsFor(j.ASA, asatest.PathGames)); got < 3 {
		t.Fatalf("ASA saw %d games requests, want the failing attempts", got)
	}

	// /cache/status still serves the last good sync. The plan expects it to
	// report the failed attempt as well, but a failed source operation is
	// recorded only in the source audit tables, never in sync_runs, which is
	// all /cache/status reads; cache.DB.RecordFailure, which would write a
	// failed run there, has no caller.
	failed := readCacheStatus(t, j.fixture)
	if !failed.OK {
		t.Errorf("/cache/status ok = false after the failure")
	}
	if failed.LastSuccess == nil || good.LastSuccess == nil || failed.LastSuccess.ID != good.LastSuccess.ID {
		t.Errorf("/cache/status last_success = %+v, want it unchanged at %+v", failed.LastSuccess, good.LastSuccess)
	}

	// Desktop only: the invariant is the data, and J1 checks every page on a phone.
	for _, vp := range []viewport{Desktop} {
		t.Run("after failure "+vp.Name, func(t *testing.T) {
			page := newPage(t, vp)
			visit(t, page, j.URL(""))
			rows := readStandings(t, page)
			assertRecords(t, rows, before)
			if row := rowOf(t, rows, teamZero); strings.Contains(row.Badge, "Playoffs") {
				t.Errorf("a playoffs indicator appeared although the new result was never fetched: %+v", row)
			}
			assertOthersClinched(t, rows)
			assertNoHorizontalOverflow(t, page)

			visit(t, page, j.URL("seasons/"+currentSeason+"/fixtures"))
			if got := fixtureCount(t, page); got != fixtureGames {
				t.Errorf("fixtures page lists %d fixtures, want %d", got, fixtureGames)
			}
			visit(t, page, j.URL("seasons/"+currentSeason+"/clinching"))
			if clinchedOnPage(t, page, teamZeroNm) {
				t.Error("clinching page shows the result that was never fetched")
			}
			if !clinchedOnPage(t, page, "Team 1 FC") {
				t.Error("clinching page lost the earlier clinched entries")
			}
		})
	}

	// ASA recovers: the failed operation was not marked done, so the next
	// check fetches the result.
	j.sync(t)
	recovered := readCacheStatus(t, j.fixture)
	if recovered.LastAttempt == nil || recovered.LastAttempt.Outcome != "success" {
		t.Errorf("after recovery /cache/status last_attempt = %+v, want success", recovered.LastAttempt)
	}
	t.Run("after recovery", func(t *testing.T) {
		checkTeamZero(t, j, true, Desktop)
	})
}

// TestJ7SchedulerPath is journey J7: with the scheduler running on a short
// interval and an injected clock, a result that becomes due is picked up without
// CheckNow.
func TestJ7SchedulerPath(t *testing.T) {
	t.Parallel()
	cfg := lateSeason()
	cfg.startScheduler = true
	cfg.checkInterval = 20 * time.Millisecond
	j := newJourney(t, cfg)
	assertLateSeasonArithmetic(t, j)

	// Start only after the cache is filled, so the startup bootstrap has no
	// catalog loading left to wait for. From here on the test never calls
	// CheckNow.
	startupPlanning, resumeStartup := j.Clock.holdNextRead(t)
	j.Server.Start()
	select {
	case <-startupPlanning:
	// Startup recalculates cached clinching before reading the planning clock;
	// allow the same budget as publishing below, including under the race detector.
	case <-time.After(60 * time.Second):
		t.Fatal("scheduler did not reach startup planning")
	}

	// The result is at ASA, but the game is not due until the clock passes its
	// kickoff plus the 2h completion grace. Startup already captured the old
	// time, so it cannot fetch this result; a periodic tick must pick it up.
	j.upsert(winFor(j.pendingGameOf(t, teamZero), teamZero))
	j.Clock.Advance(lateRoundsDue)
	resumeStartup()

	eventually(t, 60*time.Second, "the scheduler to publish team-0's clinch", func() bool {
		body := httpBody(t, j.fixture, "")
		i := strings.Index(body, `data-team-id="`+teamZero+`"`)
		if i < 0 {
			return false
		}
		row := body[i:]
		if end := strings.Index(row, "</tr>"); end >= 0 {
			row = row[:end]
		}
		return strings.Contains(row, "qualification-badge")
	})

	// Prove that a periodic source operation changed the fixture inputs, rather
	// than accepting a page updated by startup or the fixture's manual checks.
	db, err := cache.Open(context.Background(), j.cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	audits, err := db.SourceRefreshAudits(context.Background(), cache.SourceResourceGames, currentSeason, "Regular Season")
	if err != nil {
		t.Fatal(err)
	}
	var scheduledResult bool
	for _, audit := range audits {
		if audit.Trigger == cache.SourceTriggerScheduler && audit.Outcome == cache.SourceRefreshSuccess && audit.DownstreamInputsChanged {
			scheduledResult = true
		}
	}
	if !scheduledResult {
		t.Fatal("no successful scheduler-triggered game operation changed fixture inputs")
	}

	page := newPage(t, Desktop)
	visit(t, page, j.URL(""))
	rows := readStandings(t, page)
	assertRecords(t, rows, expectedTable(j.Teams, j.Games))
	if row := rowOf(t, rows, teamZero); !strings.Contains(row.Badge, "Playoffs") {
		t.Errorf("team-0 badge = %q, want a playoffs indicator", row.Badge)
	}
}

// TestJ5MissingXG is journey J5: when only half of the completed games have xG,
// Explore labels the teams' xG as incomplete, the season and forecast pages say
// how much xG is missing, and no page errors.
func TestJ5MissingXG(t *testing.T) {
	t.Parallel()
	j := newJourney(t, journeyConfig{
		playedRounds:   fixtureTeams - 1,
		firstPendingIn: 24 * time.Hour,
		xg:             0.5,
	})
	const withXG, completed = fixturePlayedGames / 2, fixturePlayedGames
	expect := playwright.NewPlaywrightAssertions()
	// Phone only: the text is the same at both widths, the long notices are
	// likelier to overflow here, and the Explore tests cover partial xG on desktop.
	for _, vp := range []viewport{Mobile} {
		t.Run(vp.Name, func(t *testing.T) {
			page := newPage(t, vp)
			main := page.Locator("main")
			contain := func(path string, want ...any) {
				t.Helper()
				visit(t, page, j.URL(path))
				for _, text := range want {
					if err := expect.Locator(main).ToContainText(text); err != nil {
						t.Errorf("%s should show %v: %v", path, text, err)
					}
				}
				assertNoHorizontalOverflow(t, page)
			}

			// Every team's matches are only partly covered, so no team has a
			// complete xG comparison.
			for _, display := range []string{"gap", "scatter"} {
				contain("explore?view=teams&display="+display,
					"Some match xG data is missing. Affected teams have no xG comparison yet.",
					"No teams have complete xG differential for this measure yet.")
			}
			visit(t, page, j.URL("explore?view=teams&display=quadrant&quadrant-data=xg"))
			if err := expect.Locator(page.Locator("[data-team-quadrant-missing]")).ToContainText("Incomplete xG for"); err != nil {
				t.Errorf("quadrant plot should list the teams with incomplete xG: %v", err)
			}

			contain("seasons/"+currentSeason,
				fmt.Sprintf("Incomplete xG data: xG is unavailable for %d of %d completed matches", completed-withXG, completed))
			contain("seasons/"+currentSeason+"/forecast",
				fmt.Sprintf("xG coverage: %d of %d completed matches", withXG, completed))
			contain("seasons/" + currentSeason + "/fixtures")
			contain("history")

			visit(t, page, j.URL(""))
			if rows := readStandings(t, page); len(rows) != fixtureTeams {
				t.Errorf("standings have %d rows, want %d", len(rows), fixtureTeams)
			}
		})
	}
}
