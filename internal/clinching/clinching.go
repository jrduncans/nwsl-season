package clinching

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jrduncans/nwsl-season/internal/standings"
)

const (
	MethodClinched                      = "clinched"
	MethodNotClinched                   = "not-clinched"
	MethodBlockedByDisciplinaryTieBreak = "blocked-by-disciplinary-tiebreak"
)

// Scoreline is a representative final score for a remaining game.
type Scoreline struct {
	Home int
	Away int
}

// ScenarioGame records one simulated remaining fixture result.
type ScenarioGame struct {
	GameID     string
	HomeTeamID string
	AwayTeamID string
	HomeScore  int
	AwayScore  int
}

// Scenario is a witness set of simulated results.
type Scenario struct {
	Games []ScenarioGame
}

// Result explains whether the target team has clinched.
type Result struct {
	Clinched              bool
	Method                string
	MaxTeamsAhead         int
	PlayoffPlaces         int
	TargetTeamID          string
	BlockingScenario      *Scenario
	DisciplinaryTieNote   string
	DisciplinaryTieTeamID []string
}

// Option customizes clinching evaluation.
type Option func(*config)

type config struct {
	scorelines []Scoreline
	bruteForce bool
}

// WithScorelines replaces the default representative scorelines.
func WithScorelines(scorelines []Scoreline) Option {
	return func(cfg *config) {
		cfg.scorelines = append([]Scoreline(nil), scorelines...)
	}
}

// WithBruteForce selects the exhaustive oracle enumerator.
func WithBruteForce() Option {
	return func(cfg *config) {
		cfg.bruteForce = true
	}
}

// DefaultScorelines returns the representative score universe used by Evaluate.
func DefaultScorelines() []Scoreline {
	return []Scoreline{
		{Home: 1, Away: 0},
		{Home: 2, Away: 0},
		{Home: 3, Away: 0},
		{Home: 4, Away: 0},
		{Home: 0, Away: 1},
		{Home: 0, Away: 2},
		{Home: 0, Away: 3},
		{Home: 0, Away: 4},
		{Home: 0, Away: 0},
		{Home: 1, Away: 1},
		{Home: 2, Away: 2},
		{Home: 3, Away: 3},
	}
}

// Evaluate proves whether targetTeamID has clinched one of playoffPlaces places.
func Evaluate(teams []standings.Team, games []standings.Game, targetTeamID string, playoffPlaces int, options ...Option) (Result, error) {
	cfg := config{scorelines: DefaultScorelines()}
	for _, option := range options {
		option(&cfg)
	}

	if playoffPlaces <= 0 {
		return Result{}, errors.New("playoff places must be positive")
	}
	if len(cfg.scorelines) == 0 {
		return Result{}, errors.New("at least one scoreline is required")
	}
	if !hasTeam(teams, targetTeamID) {
		return Result{}, fmt.Errorf("target team %q not found", targetTeamID)
	}

	search := newSearch(teams, games, targetTeamID, playoffPlaces, cfg.scorelines, cfg.bruteForce)
	search.run()
	return search.result(), nil
}

type search struct {
	teams         []standings.Team
	baseGames     []standings.Game
	remaining     []standings.Game
	targetTeamID  string
	playoffPlaces int
	scorelines    []Scoreline
	bruteForce    bool
	teamIDs       []string
	seenStates    map[string]bool

	maxTeamsAhead       int
	normalBlocking      *Scenario
	disciplineBlocking  *Scenario
	disciplineTieTeamID []string
}

func newSearch(teams []standings.Team, games []standings.Game, targetTeamID string, playoffPlaces int, scorelines []Scoreline, bruteForce bool) *search {
	baseGames := make([]standings.Game, 0, len(games))
	remaining := make([]standings.Game, 0, len(games))
	for _, game := range games {
		if completed(game) {
			baseGames = append(baseGames, game)
			continue
		}
		game.HomeScore = nil
		game.AwayScore = nil
		remaining = append(remaining, game)
	}

	if !bruteForce {
		sortRemaining(remaining, teams, baseGames, targetTeamID)
	}
	teamIDs := make([]string, 0, len(teams))
	for _, team := range teams {
		teamIDs = append(teamIDs, team.ID)
	}
	sort.Strings(teamIDs)

	return &search{
		teams:         append([]standings.Team(nil), teams...),
		baseGames:     baseGames,
		remaining:     remaining,
		targetTeamID:  targetTeamID,
		playoffPlaces: playoffPlaces,
		scorelines:    append([]Scoreline(nil), scorelines...),
		bruteForce:    bruteForce,
		teamIDs:       teamIDs,
		seenStates:    map[string]bool{},
	}
}

func (s *search) run() {
	s.walk(0, append([]standings.Game(nil), s.baseGames...), nil)
}

func (s *search) walk(index int, games []standings.Game, scenario []ScenarioGame) bool {
	if index == len(s.remaining) {
		s.evaluateLeaf(games, scenario)
		return false
	}
	if !s.bruteForce {
		key := s.stateKey(index, games)
		if s.seenStates[key] {
			return false
		}
		s.seenStates[key] = true
	}

	game := s.remaining[index]
	for _, scoreline := range s.candidateScorelines(game) {
		simulated := game
		simulated.Status = standings.CompletedStatus
		simulated.HomeScore = intPtr(scoreline.Home)
		simulated.AwayScore = intPtr(scoreline.Away)
		nextGames := append(games, simulated)
		nextScenario := append(scenario, ScenarioGame{
			GameID:     simulated.ID,
			HomeTeamID: simulated.HomeTeamID,
			AwayTeamID: simulated.AwayTeamID,
			HomeScore:  scoreline.Home,
			AwayScore:  scoreline.Away,
		})
		if s.walk(index+1, nextGames, nextScenario) {
			return true
		}
	}
	return false
}

func (s *search) stateKey(index int, games []standings.Game) string {
	overall := make(map[string]standings.Record, len(s.teamIDs))
	headToHead := make(map[string]standings.Record)
	for _, game := range games {
		if !completed(game) {
			continue
		}
		homeScore := *game.HomeScore
		awayScore := *game.AwayScore
		home := overall[game.HomeTeamID]
		applyRecord(&home, homeScore, awayScore)
		overall[game.HomeTeamID] = home
		away := overall[game.AwayTeamID]
		applyRecord(&away, awayScore, homeScore)
		overall[game.AwayTeamID] = away

		homeKey := game.HomeTeamID + ">" + game.AwayTeamID
		homeHead := headToHead[homeKey]
		applyRecord(&homeHead, homeScore, awayScore)
		headToHead[homeKey] = homeHead
		awayKey := game.AwayTeamID + ">" + game.HomeTeamID
		awayHead := headToHead[awayKey]
		applyRecord(&awayHead, awayScore, homeScore)
		headToHead[awayKey] = awayHead
	}

	var builder strings.Builder
	fmt.Fprintf(&builder, "%d", index)
	for _, teamID := range s.teamIDs {
		writeRecord(&builder, teamID, overall[teamID])
	}
	for _, left := range s.teamIDs {
		for _, right := range s.teamIDs {
			if left == right {
				continue
			}
			writeRecord(&builder, left+">"+right, headToHead[left+">"+right])
		}
	}
	return builder.String()
}

func (s *search) candidateScorelines(game standings.Game) []Scoreline {
	if game.HomeTeamID != s.targetTeamID && game.AwayTeamID != s.targetTeamID {
		return s.scorelines
	}

	candidates := make([]Scoreline, 0, len(s.scorelines))
	for _, scoreline := range s.scorelines {
		switch {
		case game.HomeTeamID == s.targetTeamID && scoreline.Home < scoreline.Away:
			candidates = append(candidates, scoreline)
		case game.AwayTeamID == s.targetTeamID && scoreline.Away < scoreline.Home:
			candidates = append(candidates, scoreline)
		}
	}
	return candidates
}

func (s *search) evaluateLeaf(games []standings.Game, scenario []ScenarioGame) {
	table := standings.Calculate(s.teams, games, standings.OfficialTotalRules())
	ahead := countAhead(table, s.targetTeamID)
	if ahead.total > s.maxTeamsAhead {
		s.maxTeamsAhead = ahead.total
	}
	if ahead.total < s.playoffPlaces {
		return
	}

	witness := &Scenario{Games: append([]ScenarioGame(nil), scenario...)}
	if ahead.normal >= s.playoffPlaces {
		if s.normalBlocking == nil {
			s.normalBlocking = witness
		}
		return
	}
	if s.disciplineBlocking == nil {
		s.disciplineBlocking = witness
		s.disciplineTieTeamID = ahead.disciplineTieTeamID
	}
}

func (s *search) result() Result {
	result := Result{
		Clinched:      s.maxTeamsAhead < s.playoffPlaces,
		MaxTeamsAhead: s.maxTeamsAhead,
		PlayoffPlaces: s.playoffPlaces,
		TargetTeamID:  s.targetTeamID,
		Method:        MethodClinched,
	}
	if result.Clinched {
		return result
	}

	if s.normalBlocking != nil {
		result.Method = MethodNotClinched
		result.BlockingScenario = s.normalBlocking
		return result
	}

	result.Method = MethodBlockedByDisciplinaryTieBreak
	result.BlockingScenario = s.disciplineBlocking
	result.DisciplinaryTieTeamID = append([]string(nil), s.disciplineTieTeamID...)
	result.DisciplinaryTieNote = "least disciplinary points are unavailable, so unresolved official ties conservatively block an exact clinch"
	return result
}

type aheadCount struct {
	normal              int
	total               int
	disciplineTieTeamID []string
}

func countAhead(table []standings.TableRow, targetTeamID string) aheadCount {
	seen := map[string]bool{}
	count := aheadCount{}
	var target standings.TableRow
	found := false
	for _, row := range table {
		if row.Team.ID == targetTeamID {
			target = row
			found = true
			break
		}
	}

	disciplineTied := map[string]bool{}
	if found && target.TieBreak.Undetermined {
		for _, teamID := range target.TieBreak.TiedTeamIDs {
			if teamID != targetTeamID {
				disciplineTied[teamID] = true
			}
		}
	}

	for _, row := range table {
		if row.Team.ID == targetTeamID {
			break
		}
		if !disciplineTied[row.Team.ID] {
			seen[row.Team.ID] = true
			count.normal++
		}
	}
	count.total = count.normal

	if !found || !target.TieBreak.Undetermined {
		return count
	}

	for _, teamID := range target.TieBreak.TiedTeamIDs {
		if teamID == targetTeamID {
			continue
		}
		count.disciplineTieTeamID = append(count.disciplineTieTeamID, teamID)
		if seen[teamID] {
			continue
		}
		seen[teamID] = true
		count.total++
	}
	sort.Strings(count.disciplineTieTeamID)
	return count
}

func sortRemaining(games []standings.Game, teams []standings.Team, baseGames []standings.Game, targetTeamID string) {
	table := standings.Calculate(teams, baseGames, standings.OfficialTotalRules())
	points := make(map[string]int, len(table))
	targetPoints := 0
	for _, row := range table {
		points[row.Team.ID] = row.Record.Points
		if row.Team.ID == targetTeamID {
			targetPoints = row.Record.Points
		}
	}

	sort.SliceStable(games, func(i, j int) bool {
		left := fixturePriority(games[i], points, targetPoints, targetTeamID)
		right := fixturePriority(games[j], points, targetPoints, targetTeamID)
		if left != right {
			return left < right
		}
		return strings.Compare(games[i].ID, games[j].ID) < 0
	})
}

func fixturePriority(game standings.Game, points map[string]int, targetPoints int, targetTeamID string) int {
	priority := 0
	if game.HomeTeamID == targetTeamID || game.AwayTeamID == targetTeamID {
		priority -= 1000
	}
	homeGap := abs(points[game.HomeTeamID] - targetPoints)
	awayGap := abs(points[game.AwayTeamID] - targetPoints)
	return priority + homeGap + awayGap
}

func applyRecord(record *standings.Record, goalsFor, goalsAgainst int) {
	record.Played++
	record.GoalsFor += goalsFor
	record.GoalsAgainst += goalsAgainst
	switch {
	case goalsFor > goalsAgainst:
		record.Wins++
		record.Points += 3
	case goalsFor < goalsAgainst:
		record.Losses++
	default:
		record.Draws++
		record.Points++
	}
}

func writeRecord(builder *strings.Builder, label string, record standings.Record) {
	fmt.Fprintf(builder, "|%s:%d,%d,%d,%d,%d,%d,%d",
		label,
		record.Played,
		record.Wins,
		record.Draws,
		record.Losses,
		record.GoalsFor,
		record.GoalsAgainst,
		record.Points)
}

func completed(game standings.Game) bool {
	return game.Status == standings.CompletedStatus && game.HomeScore != nil && game.AwayScore != nil
}

func hasTeam(teams []standings.Team, teamID string) bool {
	for _, team := range teams {
		if team.ID == teamID {
			return true
		}
	}
	return false
}

func intPtr(value int) *int {
	return &value
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
