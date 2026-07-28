package backtest

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/forecast"
	"github.com/jrduncans/nwsl-season/internal/simulation"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

const (
	DevelopmentWindow = "development"
	HeldoutWindow     = "held_out"
	AllStages         = "overall"
)

var stageNames = []string{"0-20%", "20-40%", "40-60%", "60-80%", "80-100%"}

// HistoricalPlayoffPlaces returns the comparable NWSL regular-season format
// used by the v1 evaluation window. The 2020 tournament season is excluded.
func HistoricalPlayoffPlaces(season string) (int, bool) {
	year, err := strconv.Atoi(season)
	if err != nil {
		return 0, false
	}
	switch {
	case year >= 2016 && year <= 2019:
		return 4, true
	case year >= 2021 && year <= 2023:
		return 6, true
	case year >= 2024 && year <= 2025:
		return 8, true
	default:
		return 0, false
	}
}

// Game is a historical fixture plus the kickoff needed to construct daily,
// leakage-safe cutoffs. The embedded standings value remains the single source
// of truth for teams, status, and scores.
type Game struct {
	standings.Game
	Kickoff time.Time
}

// Season is one independently audited regular-season evaluation unit.
type Season struct {
	ID            string
	Window        string
	PlayoffPlaces int
	Teams         []standings.Team
	Games         []Game
	XGoals        map[string]forecast.ExpectedGoals
}

type Config struct {
	Models             []forecast.Model
	IncumbentModelID   string
	ReferenceModelIDs  map[string]bool
	Iterations         int
	BootstrapResamples int
	BootstrapSeed      int64
	GeneratedAt        time.Time
	GitCommit          string
}

type scoreAccumulator struct {
	MatchLogLoss, PlayoffBrier, ShieldBrier accumulator
	PointsMAE, PointsCRPS                   accumulator
	PositionMAE, PositionRPS                accumulator
	PlayoffPredictions, ShieldPredictions   []float64
	PlayoffObserved, ShieldObserved         []bool
}

type accumulator struct {
	Sum   float64
	Count int
}

func (a *accumulator) add(value float64) { a.Sum += value; a.Count++ }
func (a accumulator) score() Score       { return Score{Mean: divide(a.Sum, a.Count), Count: a.Count} }

type modelAccumulator struct {
	windows map[string]map[string]*scoreAccumulator
	blocks  map[string]map[string]MetricSet
}

// Evaluate audits seasons, walks forward one UTC date at a time, scores every
// model, bootstraps paired final-test date blocks, and applies the precommitted
// recommendation rule.
func Evaluate(ctx context.Context, seasons []Season, cfg Config) (Report, error) {
	if err := validateConfig(cfg); err != nil {
		return Report{}, err
	}
	if len(seasons) == 0 {
		return Report{}, fmt.Errorf("at least one evaluation season is required")
	}
	report := Report{
		Status: "complete", IncumbentModel: cfg.IncumbentModelID,
		SelectedModel: cfg.IncumbentModelID, GeneratedAt: cfg.GeneratedAt.UTC(), GitCommit: cfg.GitCommit,
		Iterations: cfg.Iterations, BootstrapResamples: cfg.BootstrapResamples,
		Limitations: []string{
			"Historical ASA xG contains currently published or corrected values, not a reconstruction of when each value was first available.",
			"Daily UTC cutoffs prevent games on the same date from training one another.",
		},
	}
	for id := range cfg.ReferenceModelIDs {
		report.ReferenceModels = append(report.ReferenceModels, id)
	}
	sort.Strings(report.ReferenceModels)
	models := make(map[string]*modelAccumulator, len(cfg.Models))
	for _, model := range cfg.Models {
		models[model.Info().ID] = &modelAccumulator{windows: map[string]map[string]*scoreAccumulator{}, blocks: map[string]map[string]MetricSet{}}
	}

	validHeldoutCoverage, heldoutSeasons := true, 0
	for _, season := range seasons {
		audit, prepared, err := auditSeason(season)
		report.Seasons = append(report.Seasons, audit)
		if err != nil {
			report.Status = "incomplete"
			if season.Window == HeldoutWindow {
				validHeldoutCoverage = false
			}
			continue
		}
		if season.Window == HeldoutWindow && audit.XGCoverage < .95 {
			validHeldoutCoverage = false
		}
		if season.Window == HeldoutWindow {
			heldoutSeasons++
		}
		if err := evaluateSeason(ctx, prepared, cfg, models); err != nil {
			return Report{}, fmt.Errorf("evaluate season %s: %w", season.ID, err)
		}
	}
	if heldoutSeasons == 0 {
		validHeldoutCoverage = false
	}
	if !validHeldoutCoverage {
		report.Status = "incomplete"
	}

	for _, model := range cfg.Models {
		id := model.Info().ID
		result := ModelResult{ID: id, Name: model.Info().Name, Windows: map[string]WindowResult{}}
		for _, window := range []string{DevelopmentWindow, HeldoutWindow, "all"} {
			stages := map[string]MetricSet{}
			for _, stage := range append([]string{AllStages}, stageNames...) {
				stages[stage] = finalize(models[id].windows[window][stage])
			}
			result.Windows[window] = WindowResult{Metrics: stages[AllStages], Stages: stages}
		}
		report.Models = append(report.Models, result)
	}

	report.Comparisons = comparisons(cfg, models)
	report.Selection = selectModel(report, validHeldoutCoverage)
	report.SelectedModel = report.Selection.SelectedModel
	return report, nil
}

func validateConfig(cfg Config) error {
	if len(cfg.Models) < 1 || cfg.Iterations < 1 || cfg.BootstrapResamples < 1 {
		return fmt.Errorf("models, positive iterations, and positive bootstrap resamples are required")
	}
	seen, incumbent := map[string]bool{}, false
	for _, model := range cfg.Models {
		if model == nil || model.Info().ID == "" || seen[model.Info().ID] {
			return fmt.Errorf("models must have unique non-empty IDs")
		}
		seen[model.Info().ID] = true
		incumbent = incumbent || model.Info().ID == cfg.IncumbentModelID
	}
	if !incumbent {
		return fmt.Errorf("incumbent model %q is not configured", cfg.IncumbentModelID)
	}
	if cfg.GeneratedAt.IsZero() {
		return fmt.Errorf("generation time is required")
	}
	return nil
}

func auditSeason(season Season) (SeasonAudit, Season, error) {
	audit := SeasonAudit{Season: season.ID, Window: season.Window, Teams: len(season.Teams), Games: len(season.Games), PlayoffPlaces: season.PlayoffPlaces}
	fail := func(format string, args ...any) (SeasonAudit, Season, error) {
		audit.Included = false
		audit.ExclusionReason = fmt.Sprintf(format, args...)
		return audit, Season{}, fmt.Errorf("%s", audit.ExclusionReason)
	}
	if season.ID == "" || (season.Window != DevelopmentWindow && season.Window != HeldoutWindow) {
		return fail("season ID and evaluation window are required")
	}
	if len(season.Teams) < 2 || season.PlayoffPlaces < 1 || season.PlayoffPlaces >= len(season.Teams) {
		return fail("invalid team or playoff-place count")
	}
	knownTeams := map[string]bool{}
	for _, team := range season.Teams {
		if team.ID == "" || knownTeams[team.ID] {
			return fail("teams have an empty or duplicate ID %q", team.ID)
		}
		knownTeams[team.ID] = true
	}
	seen := map[string]bool{}
	completed := 0
	for i := range season.Games {
		game := &season.Games[i]
		if game.ID == "" || seen[game.ID] {
			return fail("empty or duplicate game ID %q", game.ID)
		}
		seen[game.ID] = true
		if !knownTeams[game.HomeTeamID] || !knownTeams[game.AwayTeamID] || game.HomeTeamID == game.AwayTeamID {
			return fail("game %q has invalid team references", game.ID)
		}
		if game.Kickoff.IsZero() {
			return fail("game %q has no parseable kickoff", game.ID)
		}
		game.Kickoff = game.Kickoff.UTC()
		switch game.Status {
		case standings.CompletedStatus:
			if game.HomeScore == nil || game.AwayScore == nil || *game.HomeScore < 0 || *game.AwayScore < 0 {
				return fail("completed game %q has an invalid score", game.ID)
			}
			completed++
		case fixtures.PreMatchStatus:
			return fail("game %q remains PreMatch", game.ID)
		case fixtures.AbandonedStatus:
		default:
			return fail("game %q has unsupported status %q", game.ID, game.Status)
		}
	}
	available := 0
	for _, game := range season.Games {
		if game.Status == standings.CompletedStatus {
			if value, ok := season.XGoals[game.ID]; ok {
				if value.GameID != "" && value.GameID != game.ID {
					return fail("xG row %q has a mismatched game ID", game.ID)
				}
				if !finiteNonNegative(value.Home) || !finiteNonNegative(value.Away) {
					return fail("xG row %q has invalid values", game.ID)
				}
				available++
			}
		}
	}
	audit.CompletedGames, audit.XGAvailable = completed, available
	if completed > 0 {
		audit.XGCoverage = float64(available) / float64(completed)
	}
	if completed == 0 {
		return fail("season has no completed games")
	}
	if len(standings.Calculate(season.Teams, standingsGames(season.Games), standings.OfficialTotalRules())) != len(season.Teams) {
		return fail("final table could not be calculated")
	}
	audit.Included = true
	return audit, season, nil
}

func evaluateSeason(ctx context.Context, season Season, cfg Config, accumulators map[string]*modelAccumulator) error {
	finalTable := standings.Calculate(season.Teams, standingsGames(season.Games), standings.OfficialTotalRules())
	actualPoints, actualPosition := map[string]int{}, map[string]int{}
	for position, row := range finalTable {
		actualPoints[row.Team.ID], actualPosition[row.Team.ID] = row.Record.Points, position+1
	}
	dates := uniqueDates(season.Games)
	for _, date := range dates {
		if err := ctx.Err(); err != nil {
			return err
		}
		cutoffGames, today, completed := cutoff(season.Games, date)
		stage := stageName(completed, countCompleted(season.Games))
		xg := cutoffXG(season.XGoals, cutoffGames)
		for _, model := range cfg.Models {
			predictor, err := model.Fit(forecast.FitInput{Teams: season.Teams, Games: cutoffGames, XGoals: xg})
			if err != nil {
				return fmt.Errorf("fit %s at %s: %w", model.Info().ID, date.Format("2006-01-02"), err)
			}
			block := &scoreAccumulator{}
			for _, game := range today {
				distribution, err := predictor.Distribution(asRemaining(game.Game))
				if err != nil {
					return fmt.Errorf("predict %s with %s: %w", game.ID, model.Info().ID, err)
				}
				block.MatchLogLoss.add(OutcomeLogLoss(distribution.Outcomes(), observedOutcome(game.Game)))
			}
			result, err := simulation.Run(ctx, simulation.Request{Teams: season.Teams, Games: cutoffGames, XGoals: xg, Model: model, Iterations: cfg.Iterations, PlayoffPlaces: season.PlayoffPlaces})
			if err != nil {
				return fmt.Errorf("simulate %s at %s: %w", model.Info().ID, date.Format("2006-01-02"), err)
			}
			sort.Slice(result.Teams, func(i, j int) bool { return result.Teams[i].Team.ID < result.Teams[j].Team.ID })
			for _, team := range result.Teams {
				points, position := actualPoints[team.Team.ID], actualPosition[team.Team.ID]
				playoff, shield := position <= season.PlayoffPlaces, position == 1
				expectedPoints, expectedPosition := 0.0, 0.0
				for _, value := range team.PointsProbability {
					expectedPoints += float64(value.Points) * value.Probability
				}
				for index, probability := range team.PositionProbability {
					expectedPosition += float64(index+1) * probability
				}
				block.PlayoffBrier.add(Brier(team.PlayoffProbability, playoff))
				block.ShieldBrier.add(Brier(team.ShieldProbability, shield))
				block.PointsMAE.add(math.Abs(expectedPoints - float64(points)))
				block.PointsCRPS.add(DiscreteCRPS(team.PointsProbability, points))
				block.PositionMAE.add(math.Abs(expectedPosition - float64(position)))
				block.PositionRPS.add(RankedProbabilityScore(team.PositionProbability, position))
				block.PlayoffPredictions = append(block.PlayoffPredictions, team.PlayoffProbability)
				block.PlayoffObserved = append(block.PlayoffObserved, playoff)
				block.ShieldPredictions = append(block.ShieldPredictions, team.ShieldProbability)
				block.ShieldObserved = append(block.ShieldObserved, shield)
			}
			ma := accumulators[model.Info().ID]
			for _, window := range []string{season.Window, "all"} {
				merge(scoreBucket(ma, window, AllStages), block)
				merge(scoreBucket(ma, window, stage), block)
			}
			key := season.ID + "/" + date.Format("2006-01-02")
			if ma.blocks[season.Window] == nil {
				ma.blocks[season.Window] = map[string]MetricSet{}
			}
			ma.blocks[season.Window][key] = finalize(block)
		}
	}
	return nil
}

func scoreBucket(model *modelAccumulator, window, stage string) *scoreAccumulator {
	if model.windows[window] == nil {
		model.windows[window] = map[string]*scoreAccumulator{}
	}
	if model.windows[window][stage] == nil {
		model.windows[window][stage] = &scoreAccumulator{}
	}
	return model.windows[window][stage]
}

func merge(dst, src *scoreAccumulator) {
	for _, pair := range [][2]*accumulator{{&dst.MatchLogLoss, &src.MatchLogLoss}, {&dst.PlayoffBrier, &src.PlayoffBrier}, {&dst.ShieldBrier, &src.ShieldBrier}, {&dst.PointsMAE, &src.PointsMAE}, {&dst.PointsCRPS, &src.PointsCRPS}, {&dst.PositionMAE, &src.PositionMAE}, {&dst.PositionRPS, &src.PositionRPS}} {
		pair[0].Sum += pair[1].Sum
		pair[0].Count += pair[1].Count
	}
	dst.PlayoffPredictions = append(dst.PlayoffPredictions, src.PlayoffPredictions...)
	dst.PlayoffObserved = append(dst.PlayoffObserved, src.PlayoffObserved...)
	dst.ShieldPredictions = append(dst.ShieldPredictions, src.ShieldPredictions...)
	dst.ShieldObserved = append(dst.ShieldObserved, src.ShieldObserved...)
}

func finalize(value *scoreAccumulator) MetricSet {
	if value == nil {
		return MetricSet{PlayoffCalibration: Calibration(nil, nil), ShieldCalibration: Calibration(nil, nil)}
	}
	return MetricSet{
		MatchLogLoss: value.MatchLogLoss.score(), PlayoffBrier: value.PlayoffBrier.score(), ShieldBrier: value.ShieldBrier.score(),
		PointsMAE: value.PointsMAE.score(), PointsCRPS: value.PointsCRPS.score(), PositionMAE: value.PositionMAE.score(), PositionRPS: value.PositionRPS.score(),
		PlayoffCalibration: Calibration(value.PlayoffPredictions, value.PlayoffObserved), ShieldCalibration: Calibration(value.ShieldPredictions, value.ShieldObserved),
	}
}

func cutoff(games []Game, date time.Time) ([]standings.Game, []Game, int) {
	values := make([]standings.Game, 0, len(games))
	today, completed := []Game{}, 0
	for _, historical := range games {
		game := historical.Game
		day := utcDate(historical.Kickoff)
		if game.Status == standings.CompletedStatus && day.Before(date) {
			completed++
		} else if game.Status == standings.CompletedStatus {
			game = asRemaining(game)
			if day.Equal(date) {
				today = append(today, historical)
			}
		}
		values = append(values, game)
	}
	sort.Slice(values, func(i, j int) bool { return values[i].ID < values[j].ID })
	sort.Slice(today, func(i, j int) bool { return today[i].ID < today[j].ID })
	return values, today, completed
}

func cutoffXG(all map[string]forecast.ExpectedGoals, games []standings.Game) map[string]forecast.ExpectedGoals {
	values := map[string]forecast.ExpectedGoals{}
	for _, game := range games {
		if game.Status == standings.CompletedStatus {
			if value, ok := all[game.ID]; ok {
				values[game.ID] = value
			}
		}
	}
	return values
}

func uniqueDates(games []Game) []time.Time {
	seen := map[time.Time]bool{}
	for _, game := range games {
		if game.Status == standings.CompletedStatus {
			seen[utcDate(game.Kickoff)] = true
		}
	}
	values := make([]time.Time, 0, len(seen))
	for value := range seen {
		values = append(values, value)
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Before(values[j]) })
	return values
}

func utcDate(value time.Time) time.Time {
	value = value.UTC()
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
}

func stageName(completed, total int) string {
	if total <= 0 {
		return stageNames[0]
	}
	index := int(5 * float64(completed) / float64(total))
	if index > 4 {
		index = 4
	}
	return stageNames[index]
}

func standingsGames(games []Game) []standings.Game {
	values := make([]standings.Game, len(games))
	for i := range games {
		values[i] = games[i].Game
	}
	return values
}

func countCompleted(games []Game) int {
	count := 0
	for _, game := range games {
		if game.Status == standings.CompletedStatus {
			count++
		}
	}
	return count
}

func asRemaining(game standings.Game) standings.Game {
	game.Status, game.HomeScore, game.AwayScore = fixtures.PreMatchStatus, nil, nil
	return game
}

func observedOutcome(game standings.Game) string {
	if *game.HomeScore > *game.AwayScore {
		return "h"
	}
	if *game.HomeScore < *game.AwayScore {
		return "a"
	}
	return "d"
}

func divide(sum float64, count int) float64 {
	if count == 0 {
		return 0
	}
	return sum / float64(count)
}

func finiteNonNegative(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}
