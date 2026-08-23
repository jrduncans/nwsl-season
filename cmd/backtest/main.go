package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jrduncans/nwsl-season/internal/backtest"
	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/config"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/forecast"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

const (
	defaultSeasons            = "2016,2017,2018,2019,2021,2022,2023,2024,2025"
	defaultDevelopmentSeasons = "2017,2019,2022,2024"
	defaultHeldoutSeasons     = "2016,2018,2021,2023,2025"
)

func main() {
	if wantsHelp(os.Args[1:]) {
		if err := run(context.Background(), os.Args[1:], filepath.Join("data", "nwsl-season.sqlite"), os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "backtest: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if err := config.LoadEnvironmentFile(); err != nil {
		fmt.Fprintf(os.Stderr, "backtest: load environment: %v\n", err)
		os.Exit(1)
	}
	cfg, err := config.FromEnvironment()
	if err != nil {
		fmt.Fprintf(os.Stderr, "backtest: configuration: %v\n", err)
		os.Exit(1)
	}
	if err := run(context.Background(), os.Args[1:], cfg.DBPath, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "backtest: %v\n", err)
		os.Exit(1)
	}
}

func wantsHelp(args []string) bool {
	for _, arg := range args {
		if arg == "-h" || arg == "--help" {
			return true
		}
	}
	return false
}

func run(ctx context.Context, args []string, defaultDB string, stdout io.Writer) error {
	flags := flag.NewFlagSet("backtest", flag.ContinueOnError)
	flags.SetOutput(stdout)
	dbPath := flags.String("db", defaultDB, "SQLite cache database path")
	stage := flags.String("stage", "Regular Season", "competition stage")
	seasonList := flags.String("seasons", defaultSeasons, "comma-separated seasons")
	development := flags.String("development", defaultDevelopmentSeasons, "development seasons used to design and tune candidate model versions")
	heldout := flags.String("held-out", defaultHeldoutSeasons, "final-test seasons held out from model design")
	scoreSeasonsValue := flags.String("score-seasons", "", "diagnostic-only comma-separated target seasons to score while retaining all requested seasons as chronological history")
	comparisonWindow := flags.String("comparison-window", "", "diagnostic paired-comparison window; filtered runs require development")
	iterations := flags.Int("iterations", 20000, "season simulations per daily cutoff")
	resamples := flags.Int("bootstrap-resamples", 10000, "paired bootstrap resamples")
	seed := flags.Int64("bootstrap-seed", 20251109, "paired bootstrap seed")
	incumbent := flags.String("incumbent", forecast.Default().Model.Info().ID, "model ID used as the paired-comparison baseline")
	generated := flags.String("generated-at", "", "fixed RFC3339 report timestamp")
	jsonPath := flags.String("json", "docs/model-evaluation-v1.json", "JSON report path")
	markdownPath := flags.String("markdown", "docs/model-evaluation-v1.md", "Markdown report path")
	allowIncomplete := flags.Bool("allow-incomplete", false, "write an incomplete diagnostic report when requested seasons are missing, invalid, or fail the final-test xG coverage gate")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	generatedAt := time.Now().UTC()
	if *generated != "" {
		var err error
		generatedAt, err = time.Parse(time.RFC3339, *generated)
		if err != nil {
			return fmt.Errorf("parse generated-at: %w", err)
		}
	}
	seasonIDs, err := csvSet(*seasonList)
	if err != nil {
		return fmt.Errorf("seasons: %w", err)
	}
	developmentSet, err := csvSet(*development)
	if err != nil {
		return fmt.Errorf("development: %w", err)
	}
	heldoutSet, err := csvSet(*heldout)
	if err != nil {
		return fmt.Errorf("held-out: %w", err)
	}
	var scoreSeasons map[string]bool
	if *scoreSeasonsValue != "" {
		scoreSeasons, err = csvSet(*scoreSeasonsValue)
		if err != nil {
			return fmt.Errorf("score-seasons: %w", err)
		}
	}
	if len(scoreSeasons) > 0 && *comparisonWindow != backtest.DevelopmentWindow {
		return fmt.Errorf("filtered diagnostic scoring requires -comparison-window %s", backtest.DevelopmentWindow)
	}
	for id := range developmentSet {
		if heldoutSet[id] {
			return fmt.Errorf("season %s appears in both evaluation windows", id)
		}
	}

	db, err := cache.Open(ctx, *dbPath)
	if err != nil {
		return fmt.Errorf("open cache %q: %w", *dbPath, err)
	}
	defer func() { _ = db.Close() }()
	ids := setKeys(seasonIDs)
	seasons := make([]backtest.Season, 0, len(ids))
	for _, id := range ids {
		window := ""
		if developmentSet[id] {
			window = backtest.DevelopmentWindow
		}
		if heldoutSet[id] {
			window = backtest.HeldoutWindow
		}
		if window == "" {
			return fmt.Errorf("season %s is not assigned to an evaluation window", id)
		}
		places, ok := backtest.HistoricalPlayoffPlaces(id)
		if !ok {
			return fmt.Errorf("season %s has no historical playoff rules", id)
		}
		data, err := db.Season(ctx, id, *stage)
		if err != nil {
			return fmt.Errorf("load %s: %w", id, err)
		}
		season, err := evaluationSeason(id, window, places, data)
		if err != nil {
			return fmt.Errorf("prepare %s: %w", id, err)
		}
		seasons = append(seasons, season)
	}
	catalog := forecast.EvaluationCatalog()
	models := make([]forecast.Model, 0, len(catalog))
	for _, entry := range catalog {
		models = append(models, entry.Model)
	}
	report, err := backtest.Evaluate(ctx, seasons, backtest.Config{
		Models: models, IncumbentModelID: *incumbent, Iterations: *iterations,
		ReferenceModelIDs:  map[string]bool{"straight-line-pace-v1": true},
		ScoreSeasons:       scoreSeasons,
		ComparisonWindow:   *comparisonWindow,
		BootstrapResamples: *resamples, BootstrapSeed: *seed, GeneratedAt: generatedAt, GitCommit: gitCommit(),
	})
	if err != nil {
		return err
	}
	if !*allowIncomplete {
		if err := requireCompleteEvaluation(report); err != nil {
			return err
		}
	}
	jsonValue, err := backtest.JSON(report)
	if err != nil {
		return fmt.Errorf("render JSON: %w", err)
	}
	if err := writeFile(*jsonPath, jsonValue); err != nil {
		return err
	}
	if err := writeFile(*markdownPath, []byte(backtest.Markdown(report))); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stdout, "Evaluated %d seasons; selected %s.\nWrote %s and %s.\n", len(seasons), report.SelectedModel, *jsonPath, *markdownPath); err != nil {
		return fmt.Errorf("write summary: %w", err)
	}
	return nil
}

func requireCompleteEvaluation(report backtest.Report) error {
	problems := []string{}
	for _, season := range report.Seasons {
		if !season.Included {
			problems = append(problems, fmt.Sprintf("%s: %s", season.Season, season.ExclusionReason))
		}
	}
	if !report.Selection.CoverageGate {
		problems = append(problems, "final-test seasons did not all pass the 95% xG coverage gate")
	}
	if report.Status != "complete" && len(problems) == 0 {
		problems = append(problems, "evaluation did not complete")
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("evaluation data is incomplete (%s); run `make backfill-evaluation-data` and retry, or use -allow-incomplete to write an incomplete diagnostic report", strings.Join(problems, "; "))
}

func evaluationSeason(id, window string, playoffPlaces int, data cache.SeasonData) (backtest.Season, error) {
	games := make([]backtest.Game, 0, len(data.Games))
	for _, value := range data.Games {
		kickoff, err := fixtures.ParseKickoff(value.KickoffUTC)
		if err != nil {
			return backtest.Season{}, fmt.Errorf("game %s: %w", value.ASAID, err)
		}
		game := standings.Game{ID: value.ASAID, Status: value.Status, HomeTeamID: value.HomeTeamID, AwayTeamID: value.AwayTeamID, Kickoff: kickoff}
		if value.HomeScore.Valid {
			score := int(value.HomeScore.Int64)
			game.HomeScore = &score
		}
		if value.AwayScore.Valid {
			score := int(value.AwayScore.Int64)
			game.AwayScore = &score
		}
		games = append(games, backtest.Game{Game: game, Kickoff: kickoff})
	}
	xg := map[string]forecast.ExpectedGoals{}
	for _, value := range data.XGoals {
		if value.Availability == cache.XGAvailable && value.HomeXG.Valid && value.AwayXG.Valid {
			xg[value.GameID] = forecast.ExpectedGoals{GameID: value.GameID, Home: value.HomeXG.Float64, Away: value.AwayXG.Float64}
		}
	}
	return backtest.Season{ID: id, Window: window, PlayoffPlaces: playoffPlaces, Teams: data.Teams, Games: games, XGoals: xg}, nil
}

func csvSet(value string) (map[string]bool, error) {
	result := map[string]bool{}
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			return nil, fmt.Errorf("empty list item")
		}
		if result[item] {
			return nil, fmt.Errorf("duplicate %q", item)
		}
		result[item] = true
	}
	return result, nil
}

func setKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func writeFile(path string, value []byte) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return fmt.Errorf("create report directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".backtest-*")
	if err != nil {
		return fmt.Errorf("create temporary report: %w", err)
	}
	name := temporary.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := temporary.Write(value); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write report: %w", err)
	}
	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("set report permissions: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close report: %w", err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("replace report %q: %w", path, err)
	}
	return nil
}

func gitCommit() string {
	command := exec.CommandContext(context.Background(), "git", "rev-parse", "HEAD")
	command.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null")
	value, err := command.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(value))
}
