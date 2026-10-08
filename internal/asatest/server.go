// Package asatest provides a fake American Soccer Analysis API for tests.
//
// The fake serves the three endpoints the asa.Client uses, applies the same
// query-parameter filtering as the real API, and encodes responses with the
// asa wire types so it cannot drift from the client. Its state can be changed
// while it runs and every method is safe for concurrent use.
package asatest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/asa"
)

const (
	// PathTeams, PathGames and PathXGoals are the endpoints the fake serves.
	PathTeams  = "/nwsl/teams"
	PathGames  = "/nwsl/games"
	PathXGoals = "/nwsl/games/xgoals"

	// DefaultStage is the stage of every game unless SetStage says otherwise.
	// The games wire type carries no stage, so the fake tracks it separately.
	DefaultStage = "Regular Season"

	basePrefix = "/api/v1"
	timeLayout = "2006-01-02 15:04:05 MST"
	dateLayout = "2006-01-02"
)

// Request is one request the fake received, including unknown paths.
type Request struct {
	Path  string
	Query url.Values
}

type failure struct {
	status    int
	remaining int
}

// Server is a fake ASA API backed by httptest.
type Server struct {
	srv *httptest.Server

	mu       sync.Mutex
	teams    []asa.Team
	games    []asa.Game
	xgoals   []asa.GameXGoals
	stages   map[string]string
	failures map[string][]*failure
	down     bool
	requests []Request
}

// New starts a fake ASA server that is closed when the test finishes.
func New(tb testing.TB) *Server {
	tb.Helper()
	s := &Server{stages: map[string]string{}, failures: map[string][]*failure{}}
	s.srv = httptest.NewServer(http.HandlerFunc(s.handle))
	tb.Cleanup(s.srv.Close)
	return s
}

// URL is the base URL to give asa.Client.BaseURL. It mirrors the production
// "/api/v1" prefix; requests without the prefix are served too.
func (s *Server) URL() string { return s.srv.URL + basePrefix }

// Client returns an asa.Client pointed at the fake with retries disabled.
func (s *Server) Client() asa.Client {
	return asa.Client{BaseURL: s.URL(), HTTPClient: s.srv.Client(), RetryDelays: []time.Duration{}}
}

// SetTeams replaces the served teams.
func (s *Server) SetTeams(teams []asa.Team) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.teams = slices.Clone(teams)
}

// SetGames replaces the served games. Stages set earlier are kept.
func (s *Server) SetGames(games []asa.Game) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.games = slices.Clone(games)
}

// SetXGoals replaces the served game-level xG observations.
func (s *Server) SetXGoals(xgoals []asa.GameXGoals) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.xgoals = slices.Clone(xgoals)
}

// SetStage sets the stage name the stage_name filter matches for a game.
func (s *Server) SetStage(gameID, stage string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stages[gameID] = stage
}

// UpsertGame replaces the game with the same game_id, or appends it.
func (s *Server) UpsertGame(game asa.Game) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i := slices.IndexFunc(s.games, func(g asa.Game) bool { return g.GameID == game.GameID }); i >= 0 {
		s.games[i] = game
		return
	}
	s.games = append(s.games, game)
}

// Load replaces teams, games, xG and stages with the scenario's data.
func (s *Server) Load(scenario *Scenario) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.teams = slices.Clone(scenario.Teams)
	s.games = slices.Clone(scenario.Games)
	s.xgoals = slices.Clone(scenario.XGoals)
	s.stages = map[string]string{}
	for id, stage := range scenario.Stages {
		s.stages[id] = stage
	}
}

// FailNext makes the next n requests to path (for example PathGames) answer
// with status instead of data. Failures queue in call order.
func (s *Server) FailNext(path string, status, n int) {
	if n <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures[path] = append(s.failures[path], &failure{status: status, remaining: n})
}

// SetDown makes every request fail at the transport level by closing the
// connection without a response, until it is called again with false.
func (s *Server) SetDown(down bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.down = down
}

// Requests returns every request received since the last ResetRequests,
// including down, failed and unknown-path requests.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Request, len(s.requests))
	for i, r := range s.requests {
		out[i] = Request{Path: r.Path, Query: cloneValues(r.Query)}
	}
	return out
}

// ResetRequests clears the recorded requests.
func (s *Server) ResetRequests() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = nil
}

func cloneValues(v url.Values) url.Values {
	out := make(url.Values, len(v))
	for k, vals := range v {
		out[k] = slices.Clone(vals)
	}
	return out
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, basePrefix)
	query := r.URL.Query()

	s.mu.Lock()
	s.requests = append(s.requests, Request{Path: path, Query: cloneValues(query)})
	down := s.down
	var injected int
	if queue := s.failures[path]; len(queue) > 0 {
		injected = queue[0].status
		queue[0].remaining--
		if queue[0].remaining == 0 {
			s.failures[path] = queue[1:]
		}
	}
	s.mu.Unlock()

	if down {
		dropConnection(w)
		return
	}
	if injected != 0 {
		http.Error(w, "asatest: injected failure", injected)
		return
	}
	if path != PathTeams && path != PathGames && path != PathXGoals {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var (
		body any
		err  error
	)
	switch path {
	case PathTeams:
		body = s.filterTeams(query)
	case PathGames:
		body, err = s.filterGames(query)
	case PathXGoals:
		body, err = s.filterXGoals(query)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(body); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func dropConnection(w http.ResponseWriter) {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "asatest: down", http.StatusServiceUnavailable)
		return
	}
	conn, _, err := hijacker.Hijack()
	if err != nil {
		return
	}
	_ = conn.Close()
}

// csv parses a comma-separated parameter; nil means "no filter".
func csv(query url.Values, name string) []string {
	raw := query.Get(name)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func matches(filter []string, value string) bool {
	return filter == nil || slices.Contains(filter, value)
}

func (s *Server) filterTeams(query url.Values) []asa.Team {
	ids := csv(query, "team_id")
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []asa.Team{}
	for _, team := range s.teams {
		if matches(ids, team.TeamID) {
			out = append(out, team)
		}
	}
	return out
}

type gameFilter struct {
	gameIDs, teamIDs, seasons, stages, statuses []string
	start, end                                  time.Time
	hasStart, hasEnd                            bool
}

func parseGameFilter(query url.Values, withStatus bool) (gameFilter, error) {
	f := gameFilter{
		gameIDs: csv(query, "game_id"),
		teamIDs: csv(query, "team_id"),
		seasons: csv(query, "season_name"),
		stages:  csv(query, "stage_name"),
	}
	if withStatus {
		f.statuses = csv(query, "status")
	}
	var err error
	if raw := query.Get("start_date"); raw != "" {
		if f.start, err = time.Parse(dateLayout, raw); err != nil {
			return f, fmt.Errorf("invalid start_date %q: want YYYY-MM-DD", raw)
		}
		f.hasStart = true
	}
	if raw := query.Get("end_date"); raw != "" {
		if f.end, err = time.Parse(dateLayout, raw); err != nil {
			return f, fmt.Errorf("invalid end_date %q: want YYYY-MM-DD", raw)
		}
		f.hasEnd = true
	}
	if f.seasons != nil && (f.hasStart || f.hasEnd) {
		return f, fmt.Errorf("season_name cannot be combined with a date range")
	}
	return f, nil
}

func (f gameFilter) match(game asa.Game, stage string) bool {
	if !matches(f.gameIDs, game.GameID) || !matches(f.seasons, game.SeasonName) ||
		!matches(f.stages, stage) || !matches(f.statuses, game.Status) {
		return false
	}
	if f.teamIDs != nil && !slices.Contains(f.teamIDs, game.HomeTeamID) && !slices.Contains(f.teamIDs, game.AwayTeamID) {
		return false
	}
	if f.hasStart || f.hasEnd {
		kickoff, err := time.Parse(timeLayout, game.DateTimeUTC)
		if err != nil {
			return false
		}
		day := time.Date(kickoff.Year(), kickoff.Month(), kickoff.Day(), 0, 0, 0, 0, time.UTC)
		if f.hasStart && day.Before(f.start) || f.hasEnd && day.After(f.end) {
			return false
		}
	}
	return true
}

func (s *Server) stageLocked(gameID string) string {
	if stage, ok := s.stages[gameID]; ok {
		return stage
	}
	return DefaultStage
}

func (s *Server) filterGames(query url.Values) ([]asa.Game, error) {
	f, err := parseGameFilter(query, true)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []asa.Game{}
	for _, game := range s.games {
		if f.match(game, s.stageLocked(game.GameID)) {
			out = append(out, game)
		}
	}
	return out, nil
}

// filterXGoals applies the game-level filters through each observation's
// game, because the xG wire type carries no season, stage or kickoff.
func (s *Server) filterXGoals(query url.Values) ([]asa.GameXGoals, error) {
	f, err := parseGameFilter(query, false)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	games := make(map[string]asa.Game, len(s.games))
	for _, game := range s.games {
		games[game.GameID] = game
	}
	needsGame := f.seasons != nil || f.stages != nil || f.teamIDs != nil || f.hasStart || f.hasEnd
	out := []asa.GameXGoals{}
	for _, xg := range s.xgoals {
		if !matches(f.gameIDs, xg.GameID) {
			continue
		}
		if needsGame {
			game, ok := games[xg.GameID]
			if !ok || !f.match(game, s.stageLocked(game.GameID)) {
				continue
			}
		}
		out = append(out, xg)
	}
	return out, nil
}
