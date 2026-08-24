// Package simulation runs complete, internally consistent season forecasts.
// It is independent of HTTP, SQLite, and the source-data cache.
package simulation

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strings"

	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/forecast"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

// RemainingStatus is the source status for an unplayed fixture.
const RemainingStatus = fixtures.PreMatchStatus

const conditionalAttempts = 10000

// Outcome is a visitor's fixed match result.
type Outcome string

const (
	HomeWin Outcome = "h"
	Draw    Outcome = "d"
	AwayWin Outcome = "a"
)

// Valid reports whether o is a supported fixed result.
func (o Outcome) Valid() bool { return o == HomeWin || o == Draw || o == AwayWin }

// Request describes one reproducible forecast.
type Request struct {
	Teams             []standings.Team
	Games             []standings.Game
	XGoals            map[string]forecast.ExpectedGoals
	HistoricalGames   []standings.Game
	HistoricalXGoals  map[string]forecast.ExpectedGoals
	HistoricalSeasons []forecast.HistoricalSeason
	HistoricalVenue   forecast.VenueSample
	Model             forecast.Model
	Fixed             map[string]Outcome
	Iterations        int
	PlayoffPlaces     int
}

// TeamResult contains probability and uncertainty aggregates for one team.
type TeamResult struct {
	Team                standings.Team
	ExpectedPoints      float64
	PointsLow           int
	PointsHigh          int
	PointsProbability   []PointsProbability
	ExpectedPosition    float64
	PositionLow         int
	PositionHigh        int
	PositionProbability []float64
	TopFourProbability  float64
	PlayoffProbability  float64
	ShieldProbability   float64
}

type PointsProbability struct {
	Points      int
	Probability float64
}

// Result is a complete forecast result.
type Result struct {
	Model      forecast.Info
	Iterations int
	Seed       uint64
	FixedCount int
	Remaining  int
	Teams      []TeamResult
}

type preparedFixture struct {
	game         standings.Game
	distribution forecast.Distribution
	fixed        Outcome
}

type accumulator struct {
	team      standings.Team
	points    map[int]float64
	positions []float64
}

// Run fits the model once and simulates the requested number of seasons.
func Run(ctx context.Context, request Request) (Result, error) {
	prepared, err := prepare(request)
	if err != nil {
		return Result{}, err
	}
	predictor, err := request.Model.Fit(forecast.FitInput{Teams: request.Teams, Games: request.Games, XGoals: request.XGoals, HistoricalGames: request.HistoricalGames, HistoricalXGoals: request.HistoricalXGoals, HistoricalSeasons: request.HistoricalSeasons, HistoricalVenue: request.HistoricalVenue})
	if err != nil {
		return Result{}, fmt.Errorf("fit forecast model: %w", err)
	}
	for index := range prepared.remaining {
		distribution, err := predictor.Distribution(prepared.remaining[index].game)
		if err != nil {
			return Result{}, fmt.Errorf("forecast fixture %q: %w", prepared.remaining[index].game.ID, err)
		}
		prepared.remaining[index].distribution = distribution
	}

	seed := SeedWithMaterial(request.Model.Info().ID, request.Teams, request.Games, request.Fixed, predictor.SeedMaterial())
	// #nosec G404 G115 -- a stable seed makes equivalent cached forecasts reproducible; the signed conversion preserves its bits.
	rng := rand.New(rand.NewSource(int64(seed)))
	byID := make(map[string]*accumulator, len(request.Teams))
	for _, team := range request.Teams {
		byID[team.ID] = &accumulator{team: team, points: map[int]float64{}, positions: make([]float64, len(request.Teams))}
	}

	if len(prepared.remaining) == 0 {
		table := standings.Calculate(request.Teams, prepared.completed, standings.OfficialTotalRules())
		accumulateTable(table, byID, request.PlayoffPlaces, float64(request.Iterations))
	} else {
		for iteration := 0; iteration < request.Iterations; iteration++ {
			if iteration%100 == 0 {
				if err := ctx.Err(); err != nil {
					return Result{}, err
				}
			}
			games, err := simulatedGames(prepared.completed, prepared.remaining, rng)
			if err != nil {
				return Result{}, err
			}
			table := standings.Calculate(request.Teams, games, standings.OfficialTotalRules())
			accumulateTable(table, byID, request.PlayoffPlaces, 1)
		}
	}

	rows := make([]TeamResult, 0, len(request.Teams))
	for _, accumulator := range byID {
		rows = append(rows, resultFromAccumulator(accumulator, request.Iterations, request.PlayoffPlaces))
	}
	sortTeamResults(rows)
	return Result{
		Model:      request.Model.Info(),
		Iterations: request.Iterations,
		Seed:       seed,
		FixedCount: len(request.Fixed),
		Remaining:  len(prepared.remaining),
		Teams:      rows,
	}, nil
}

func sortTeamResults(rows []TeamResult) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].ExpectedPoints != rows[j].ExpectedPoints {
			return rows[i].ExpectedPoints > rows[j].ExpectedPoints
		}
		if rows[i].PlayoffProbability != rows[j].PlayoffProbability {
			return rows[i].PlayoffProbability > rows[j].PlayoffProbability
		}
		if rows[i].TopFourProbability != rows[j].TopFourProbability {
			return rows[i].TopFourProbability > rows[j].TopFourProbability
		}
		left, right := standings.DisplayName(rows[i].Team), standings.DisplayName(rows[j].Team)
		if left != right {
			return left < right
		}
		return rows[i].Team.ID < rows[j].Team.ID
	})
}

type preparedSeason struct {
	completed []standings.Game
	remaining []preparedFixture
}

func prepare(request Request) (preparedSeason, error) {
	if request.Model == nil {
		return preparedSeason{}, errors.New("forecast model is required")
	}
	if request.Iterations <= 0 {
		return preparedSeason{}, errors.New("forecast iterations must be positive")
	}
	if len(request.Teams) == 0 {
		return preparedSeason{}, errors.New("forecast teams are required")
	}
	if request.PlayoffPlaces < 1 || request.PlayoffPlaces > len(request.Teams) {
		return preparedSeason{}, fmt.Errorf("invalid playoff places %d", request.PlayoffPlaces)
	}
	teams := make(map[string]struct{}, len(request.Teams))
	for _, team := range request.Teams {
		if team.ID == "" {
			return preparedSeason{}, errors.New("forecast team has an empty ID")
		}
		if _, exists := teams[team.ID]; exists {
			return preparedSeason{}, fmt.Errorf("duplicate forecast team %q", team.ID)
		}
		teams[team.ID] = struct{}{}
	}

	values := append([]standings.Game(nil), request.Games...)
	sort.Slice(values, func(i, j int) bool { return values[i].ID < values[j].ID })
	seen := make(map[string]struct{}, len(values))
	remainingByID := make(map[string]int)
	prepared := preparedSeason{}
	for _, game := range values {
		if game.ID == "" {
			return preparedSeason{}, errors.New("forecast game has an empty ID")
		}
		if _, exists := seen[game.ID]; exists {
			return preparedSeason{}, fmt.Errorf("duplicate forecast game %q", game.ID)
		}
		seen[game.ID] = struct{}{}
		if _, known := teams[game.HomeTeamID]; !known {
			return preparedSeason{}, fmt.Errorf("game %q references unknown home team", game.ID)
		}
		if _, known := teams[game.AwayTeamID]; !known {
			return preparedSeason{}, fmt.Errorf("game %q references unknown away team", game.ID)
		}
		switch game.Status {
		case standings.CompletedStatus:
			if game.HomeScore == nil || game.AwayScore == nil || *game.HomeScore < 0 || *game.AwayScore < 0 {
				return preparedSeason{}, fmt.Errorf("completed game %q has an invalid score", game.ID)
			}
			prepared.completed = append(prepared.completed, game)
		case RemainingStatus:
			if game.HomeScore != nil || game.AwayScore != nil {
				return preparedSeason{}, fmt.Errorf("remaining game %q already has a score", game.ID)
			}
			remainingByID[game.ID] = len(prepared.remaining)
			prepared.remaining = append(prepared.remaining, preparedFixture{game: game})
		}
	}
	for id, outcome := range request.Fixed {
		if !outcome.Valid() {
			return preparedSeason{}, fmt.Errorf("invalid fixed outcome for game %q", id)
		}
		index, ok := remainingByID[id]
		if !ok {
			return preparedSeason{}, fmt.Errorf("fixed game is not remaining: %q", id)
		}
		prepared.remaining[index].fixed = outcome
	}
	return prepared, nil
}

func simulatedGames(completed []standings.Game, remaining []preparedFixture, rng *rand.Rand) ([]standings.Game, error) {
	games := make([]standings.Game, 0, len(completed)+len(remaining))
	games = append(games, completed...)
	for _, fixture := range remaining {
		score, err := sample(fixture.distribution, fixture.fixed, rng)
		if err != nil {
			return nil, fmt.Errorf("sample fixture %q: %w", fixture.game.ID, err)
		}
		home, away := score.Home, score.Away
		games = append(games, standings.Game{
			ID: fixture.game.ID, Status: standings.CompletedStatus,
			HomeTeamID: fixture.game.HomeTeamID, AwayTeamID: fixture.game.AwayTeamID,
			HomeScore: &home, AwayScore: &away,
		})
	}
	return games, nil
}

func sample(distribution forecast.Distribution, fixed Outcome, rng *rand.Rand) (forecast.Scoreline, error) {
	for attempt := 0; attempt < conditionalAttempts; attempt++ {
		score := distribution.Sample(rng)
		if fixed == "" || matches(score, fixed) {
			return score, nil
		}
	}
	return forecast.Scoreline{}, fmt.Errorf("could not sample a %s outcome after %d attempts", fixed, conditionalAttempts)
}

func matches(score forecast.Scoreline, outcome Outcome) bool {
	switch outcome {
	case HomeWin:
		return score.Home > score.Away
	case Draw:
		return score.Home == score.Away
	case AwayWin:
		return score.Home < score.Away
	default:
		return false
	}
}

func accumulateTable(table []standings.TableRow, byID map[string]*accumulator, playoffPlaces int, weight float64) {
	for _, row := range table {
		byID[row.Team.ID].points[row.Record.Points] += weight
	}
	processed := map[string]bool{}
	for index, row := range table {
		if !row.TieBreak.Undetermined {
			addPosition(byID[row.Team.ID], index, weight)
			continue
		}
		key := strings.Join(row.TieBreak.TiedTeamIDs, "\x00")
		if processed[key] {
			continue
		}
		processed[key] = true
		positions := make([]int, 0, len(row.TieBreak.TiedTeamIDs))
		for candidate, value := range table {
			if value.TieBreak.Undetermined && strings.Join(value.TieBreak.TiedTeamIDs, "\x00") == key {
				positions = append(positions, candidate)
			}
		}
		if len(positions) == 0 {
			continue
		}
		share := weight / float64(len(positions))
		for _, candidate := range positions {
			for _, position := range positions {
				addPosition(byID[table[candidate].Team.ID], position, share)
			}
		}
	}
}

func addPosition(accumulator *accumulator, position int, weight float64) {
	accumulator.positions[position] += weight
}

func resultFromAccumulator(value *accumulator, iterations, playoffPlaces int) TeamResult {
	total := float64(iterations)
	result := TeamResult{Team: value.team, PositionProbability: make([]float64, len(value.positions))}
	for points, count := range value.points {
		result.ExpectedPoints += float64(points) * count / total
	}
	result.PointsLow = weightedQuantile(value.points, total, 0.10)
	result.PointsHigh = weightedQuantile(value.points, total, 0.90)
	points := make([]int, 0, len(value.points))
	for point := range value.points {
		points = append(points, point)
	}
	sort.Ints(points)
	for _, point := range points {
		result.PointsProbability = append(result.PointsProbability, PointsProbability{Points: point, Probability: value.points[point] / total})
	}
	positionValues := make(map[int]float64, len(value.positions))
	for index, count := range value.positions {
		position := index + 1
		result.PositionProbability[index] = count / total
		result.ExpectedPosition += float64(position) * count / total
		positionValues[position] = count
		if position <= 4 {
			result.TopFourProbability += count / total
		}
		if position <= playoffPlaces {
			result.PlayoffProbability += count / total
		}
		if position == 1 {
			result.ShieldProbability += count / total
		}
	}
	result.PositionLow = weightedQuantile(positionValues, total, 0.10)
	result.PositionHigh = weightedQuantile(positionValues, total, 0.90)
	return result
}

func weightedQuantile(values map[int]float64, total, percentile float64) int {
	keys := make([]int, 0, len(values))
	for value := range values {
		keys = append(keys, value)
	}
	sort.Ints(keys)
	target := math.Ceil(total * percentile)
	if target < 1 {
		target = 1
	}
	var accumulated float64
	for _, value := range keys {
		accumulated += values[value]
		if accumulated+1e-9 >= target {
			return value
		}
	}
	return keys[len(keys)-1]
}
