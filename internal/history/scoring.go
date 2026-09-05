// Package history derives cache-only summaries for the historical archive.
package history

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
)

// MinimumSeasonMatches is the descriptive sample required for a season to be
// plotted in the league scoring comparison.
const MinimumSeasonMatches = 20

// SeasonScoring is one season's cache-derived scoring and coverage summary.
// Rates can be available for supporting data even when PlotEligible is false.
type SeasonScoring struct {
	Season                                                       string
	Lifecycle                                                    cache.SourceScopeLifecycle
	Readiness                                                    cache.SourceReadiness
	Inventory                                                    cache.InventoryCompleteness
	InventoryGames, Played, Pending, Abandoned, InvalidCompleted int
	TotalGoals                                                   int64
	GoalBins                                                     [5]int
	XGCovered, XPointsCovered                                    int
	GoalsPerMatch, XGPerMatch, GoalsMinusXGPerMatch              *float64
	PlotEligible                                                 bool
	Exclusions                                                   []string
}

// SummarizeScoring calculates deterministic league-level scoring summaries
// from one historical cache snapshot. It never changes the supplied snapshot.
func SummarizeScoring(inputs []cache.HistoricalSeason) ([]SeasonScoring, error) {
	seasons := append([]cache.HistoricalSeason(nil), inputs...)
	seenSeasons := make(map[string]struct{}, len(seasons))
	for _, input := range seasons {
		if _, ok := seenSeasons[input.Entry.Season]; ok {
			return nil, fmt.Errorf("duplicate historical season %q", input.Entry.Season)
		}
		seenSeasons[input.Entry.Season] = struct{}{}
		if err := validateInput(input); err != nil {
			return nil, err
		}
	}

	sort.Slice(seasons, func(i, j int) bool {
		left, _ := strconv.Atoi(seasons[i].Entry.Season)
		right, _ := strconv.Atoi(seasons[j].Entry.Season)
		return left < right
	})

	summaries := make([]SeasonScoring, 0, len(seasons))
	for _, input := range seasons {
		summary, err := summarizeSeason(input)
		if err != nil {
			return nil, err
		}
		summaries = append(summaries, summary)
	}
	return summaries, nil
}

func validateInput(input cache.HistoricalSeason) error {
	entry := input.Entry
	if strings.TrimSpace(entry.Season) == "" {
		return errors.New("historical season is blank")
	}
	if _, err := strconv.Atoi(entry.Season); err != nil {
		return fmt.Errorf("historical season %q is not numeric: %w", entry.Season, err)
	}
	if entry.Stage != "Regular Season" || !entry.Public || !entry.SourceAvailable || !entry.Supports(competition.CapabilityFixtures) {
		return fmt.Errorf("unsupported historical catalog entry %s %q", entry.Season, entry.Stage)
	}

	fixtureIDs := make(map[string]struct{}, len(input.Data.Games))
	for _, game := range input.Data.Games {
		if strings.TrimSpace(game.ASAID) == "" {
			return fmt.Errorf("historical season %s has a blank fixture ID", entry.Season)
		}
		if game.Season != entry.Season || game.Stage != entry.Stage {
			return fmt.Errorf("historical season %s fixture %q has scope %s %q", entry.Season, game.ASAID, game.Season, game.Stage)
		}
		if _, exists := fixtureIDs[game.ASAID]; exists {
			return fmt.Errorf("historical season %s has duplicate fixture ID %q", entry.Season, game.ASAID)
		}
		fixtureIDs[game.ASAID] = struct{}{}
	}

	xgIDs := make(map[string]struct{}, len(input.Data.XGoals))
	for _, observation := range input.Data.XGoals {
		if strings.TrimSpace(observation.GameID) == "" {
			return fmt.Errorf("historical season %s has a blank xG fixture ID", entry.Season)
		}
		if _, exists := xgIDs[observation.GameID]; exists {
			return fmt.Errorf("historical season %s has duplicate xG ID %q", entry.Season, observation.GameID)
		}
		xgIDs[observation.GameID] = struct{}{}
	}
	return nil
}

func summarizeSeason(input cache.HistoricalSeason) (SeasonScoring, error) {
	summary := SeasonScoring{
		Season:    input.Entry.Season,
		Readiness: cache.SourceReadinessUnknown,
		Inventory: cache.InventoryCompletenessUnknown,
	}
	if input.Readiness != nil {
		summary.Lifecycle = input.Readiness.Scope.Lifecycle
		summary.Readiness = input.Readiness.Readiness
		summary.Inventory = input.Readiness.Completeness
	}

	xgByGame := make(map[string]cache.GameXG, len(input.Data.XGoals))
	for _, observation := range input.Data.XGoals {
		xgByGame[observation.GameID] = observation
	}

	var totalXG float64
	for _, game := range input.Data.Games {
		summary.InventoryGames++
		if game.Status != fixtures.CompletedStatus {
			switch game.Status {
			case fixtures.AbandonedStatus:
				summary.Abandoned++
			default:
				summary.Pending++
			}
			continue
		}
		if !validScore(game) {
			summary.InvalidCompleted++
			continue
		}

		combined, err := combinedGoals(game)
		if err != nil {
			return SeasonScoring{}, fmt.Errorf("historical season %s fixture %q: %w", input.Entry.Season, game.ASAID, err)
		}
		if summary.TotalGoals > math.MaxInt64-combined {
			return SeasonScoring{}, fmt.Errorf("historical season %s total goals overflow", input.Entry.Season)
		}
		summary.TotalGoals += combined
		summary.Played++
		summary.GoalBins[goalBin(combined)]++

		if !input.Entry.Supports(competition.CapabilityXG) {
			continue
		}
		observation, found := xgByGame[game.ASAID]
		if !found || observation.Availability != cache.XGAvailable || observation.HomeTeamID != game.HomeTeamID || observation.AwayTeamID != game.AwayTeamID {
			continue
		}
		if validXGPair(observation) {
			summary.XGCovered++
			pairTotal := observation.HomeXG.Float64 + observation.AwayXG.Float64
			if !isFinite(pairTotal) || !isFinite(totalXG+pairTotal) {
				return SeasonScoring{}, fmt.Errorf("historical season %s xG total is not finite", input.Entry.Season)
			}
			totalXG += pairTotal
		}
		if validXPointsPair(observation) {
			summary.XPointsCovered++
		}
	}

	if summary.Played > 0 {
		goalsPerMatch := float64(summary.TotalGoals) / float64(summary.Played)
		summary.GoalsPerMatch = &goalsPerMatch
		if summary.XGCovered == summary.Played {
			xgPerMatch := totalXG / float64(summary.Played)
			goalsMinusXGPerMatch := goalsPerMatch - xgPerMatch
			summary.XGPerMatch = &xgPerMatch
			summary.GoalsMinusXGPerMatch = &goalsMinusXGPerMatch
		}
	}

	summary.Exclusions = scoringExclusions(summary)
	summary.PlotEligible = len(summary.Exclusions) == 0
	return summary, nil
}

func validScore(game cache.Game) bool {
	return game.HomeScore.Valid && game.AwayScore.Valid && game.HomeScore.Int64 >= 0 && game.AwayScore.Int64 >= 0
}

func combinedGoals(game cache.Game) (int64, error) {
	if game.HomeScore.Int64 > math.MaxInt64-game.AwayScore.Int64 {
		return 0, errors.New("fixture goals overflow")
	}
	return game.HomeScore.Int64 + game.AwayScore.Int64, nil
}

func goalBin(goals int64) int {
	switch {
	case goals >= 4:
		return 4
	default:
		return int(goals)
	}
}

func validXGPair(observation cache.GameXG) bool {
	return observation.HomeXG.Valid && observation.AwayXG.Valid &&
		finiteNonnegative(observation.HomeXG.Float64) && finiteNonnegative(observation.AwayXG.Float64)
}

func validXPointsPair(observation cache.GameXG) bool {
	return observation.HomeXPoints.Valid && observation.AwayXPoints.Valid &&
		validXPoints(observation.HomeXPoints.Float64) && validXPoints(observation.AwayXPoints.Float64)
}

func finiteNonnegative(value float64) bool {
	return isFinite(value) && value >= 0
}

func validXPoints(value float64) bool {
	return isFinite(value) && value >= 0 && value <= cache.MaxGameExpectedPoints
}

func isFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func scoringExclusions(summary SeasonScoring) []string {
	exclusions := make([]string, 0, 7)
	if summary.Readiness != cache.SourceReadinessAvailable {
		exclusions = append(exclusions, "source_unavailable")
	}
	if !knownLifecycle(summary.Lifecycle) {
		exclusions = append(exclusions, "lifecycle_unknown")
	}
	if summary.Lifecycle == cache.SourceScopeUpcoming {
		exclusions = append(exclusions, "upcoming")
	}
	if summary.Inventory == cache.InventoryCompletenessIncomplete {
		exclusions = append(exclusions, "inventory_incomplete")
	}
	if summary.Lifecycle == cache.SourceScopeCompleted && summary.Pending > 0 {
		exclusions = append(exclusions, "historical_results_incomplete")
	}
	if summary.InvalidCompleted > 0 {
		exclusions = append(exclusions, "invalid_completed_results")
	}
	if summary.Played < MinimumSeasonMatches {
		exclusions = append(exclusions, "below_minimum_matches")
	}
	return exclusions
}

func knownLifecycle(lifecycle cache.SourceScopeLifecycle) bool {
	switch lifecycle {
	case cache.SourceScopeUpcoming, cache.SourceScopeActive, cache.SourceScopeCompleted:
		return true
	default:
		return false
	}
}
