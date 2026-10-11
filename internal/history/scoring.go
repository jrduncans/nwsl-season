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

// ScoringBasis selects which goals and expected goals the summaries count.
// The zero value counts every shot.
type ScoringBasis int

const (
	// AllShots counts official goals and total xG, including penalty kicks.
	AllShots ScoringBasis = iota
	// NonPenalty removes ASA's penalty goals and penalty xG from each side.
	// Points, xPoints and all official results are unaffected.
	NonPenalty
)

// ExcludesPenalties reports whether the basis removes penalty kicks.
func (b ScoringBasis) ExcludesPenalties() bool { return b == NonPenalty }

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
	// Basis is the scoring basis of every goal and xG field. GoalsCovered is
	// the number of played matches that contribute to TotalGoals, GoalBins and
	// GoalsPerMatch: every played match for AllShots, and only matches with a
	// penalty split for NonPenalty. Under NonPenalty, XGCovered counts the same
	// split-covered matches.
	Basis                                           ScoringBasis
	GoalsCovered                                    int
	XGCovered, XPointsCovered                       int
	GoalsPerMatch, XGPerMatch, GoalsMinusXGPerMatch *float64
	PlotEligible                                    bool
	Exclusions                                      []string
	Teams                                           []TeamScoring
}

// SummarizeScoring calculates deterministic league-level scoring summaries
// from one historical cache snapshot, counting every shot. It never changes
// the supplied snapshot.
func SummarizeScoring(inputs []cache.HistoricalSeason) ([]SeasonScoring, error) {
	return SummarizeScoringBasis(inputs, AllShots)
}

// SummarizeScoringBasis is SummarizeScoring on the chosen basis. Under
// NonPenalty a match contributes goals and xG only when its xG is available and
// ASA supplied a penalty split; any other played match leaves the season
// incomplete, with the "incomplete_non_penalty_data" exclusion.
func SummarizeScoringBasis(inputs []cache.HistoricalSeason, basis ScoringBasis) ([]SeasonScoring, error) {
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
		summary, err := summarizeSeason(input, basis)
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
		if game.HomeTeamID == "" || game.AwayTeamID == "" || game.HomeTeamID == game.AwayTeamID {
			return fmt.Errorf("historical season %s fixture %q has invalid team identities", entry.Season, game.ASAID)
		}
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

func summarizeSeason(input cache.HistoricalSeason, basis ScoringBasis) (SeasonScoring, error) {
	summary := SeasonScoring{
		Basis:     basis,
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
	teams := make(teamScoringTotals)
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

		if _, err := combinedGoals(game); err != nil {
			return SeasonScoring{}, fmt.Errorf("historical season %s fixture %q: %w", input.Entry.Season, game.ASAID, err)
		}
		summary.Played++

		var observation cache.GameXG
		xgFound := false
		if input.Entry.Supports(competition.CapabilityXG) {
			observation, xgFound = xgByGame[game.ASAID]
			xgFound = xgFound && observation.Availability == cache.XGAvailable && observation.HomeTeamID == game.HomeTeamID && observation.AwayTeamID == game.AwayTeamID
		}
		xgValid := xgFound && validXGPair(observation)

		homeGoals, awayGoals := game.HomeScore.Int64, game.AwayScore.Int64
		homeXG, awayXG := observation.HomeXG.Float64, observation.AwayXG.Float64
		covered := true
		xgCovered := xgValid
		if basis == NonPenalty {
			split := observation.Penalties
			covered = xgValid && split != nil && validNonPenaltySplit(game, split)
			xgCovered = covered
			if covered {
				homeGoals -= int64(split.HomeGoals)
				awayGoals -= int64(split.AwayGoals)
				homeXG = math.Max(0, homeXG-split.HomeXG)
				awayXG = math.Max(0, awayXG-split.AwayXG)
			}
		}
		teams.addResult(game, homeGoals, awayGoals, covered)
		if covered {
			combined := homeGoals + awayGoals
			if summary.TotalGoals > math.MaxInt64-combined {
				return SeasonScoring{}, fmt.Errorf("historical season %s total goals overflow", input.Entry.Season)
			}
			summary.TotalGoals += combined
			summary.GoalsCovered++
			summary.GoalBins[goalBin(combined)]++
		}

		if xgCovered {
			summary.XGCovered++
			pairTotal := homeXG + awayXG
			if !isFinite(pairTotal) || !isFinite(totalXG+pairTotal) {
				return SeasonScoring{}, fmt.Errorf("historical season %s xG total is not finite", input.Entry.Season)
			}
			totalXG += pairTotal
			teams.addXG(game, homeXG, awayXG)
		}
		if xgFound && validXPointsPair(observation) {
			summary.XPointsCovered++
			teams.addXPoints(game, observation)
		}
	}

	if summary.Played > 0 && summary.GoalsCovered > 0 {
		goalsPerMatch := float64(summary.TotalGoals) / float64(summary.GoalsCovered)
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
	summary.Teams = teams.summaries()
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

// validNonPenaltySplit rejects a split that removes more goals than a side
// scored, which would make non-penalty goals negative.
func validNonPenaltySplit(game cache.Game, split *cache.PenaltySplit) bool {
	return split.HomeGoals >= 0 && split.AwayGoals >= 0 &&
		int64(split.HomeGoals) <= game.HomeScore.Int64 && int64(split.AwayGoals) <= game.AwayScore.Int64 &&
		finiteNonnegative(split.HomeXG) && finiteNonnegative(split.AwayXG)
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
	exclusions := make([]string, 0, 8)
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
	if summary.Basis == NonPenalty && summary.GoalsCovered < summary.Played {
		exclusions = append(exclusions, "incomplete_non_penalty_data")
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
