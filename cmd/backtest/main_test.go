package main

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
)

func TestRunWritesReportsFromCache(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	dbPath := filepath.Join(directory, "test.sqlite")
	db, err := cache.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	teams := make([]cache.Team, 10)
	for i := range teams {
		teams[i] = cache.Team{ASAID: string(rune('a' + i)), Name: string(rune('A' + i)), RawJSON: "{}"}
	}
	games := make([]cache.Game, 5)
	for i := range games {
		games[i] = cache.Game{
			ASAID: string(rune('1' + i)), Season: "2025", Stage: "Regular Season", KickoffUTC: time.Date(2025, 3, 1+i, 18, 0, 0, 0, time.UTC).Format(time.RFC3339),
			Status: "FullTime", HomeTeamID: teams[2*i].ASAID, AwayTeamID: teams[2*i+1].ASAID,
			HomeScore: sql.NullInt64{Int64: 1, Valid: true}, AwayScore: sql.NullInt64{Valid: true}, RawJSON: "{}",
		}
	}
	if _, err := db.ReplaceSeason(ctx, "2025", "Regular Season", teams, games, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	jsonPath, markdownPath := filepath.Join(directory, "report.json"), filepath.Join(directory, "report.md")
	var output bytes.Buffer
	err = run(ctx, []string{
		"-db", dbPath, "-seasons", "2025", "-development", "2024", "-held-out", "2025",
		"-iterations", "5", "-bootstrap-resamples", "5", "-generated-at", "2026-07-27T12:00:00Z",
		"-allow-incomplete", "-json", jsonPath, "-markdown", markdownPath,
	}, dbPath, &output)
	if err != nil {
		t.Fatal(err)
	}
	jsonValue, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	markdownValue, err := os.ReadFile(markdownPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(jsonValue), `"selected_model"`) || !strings.Contains(string(markdownValue), "# Model evaluation v1") || !strings.Contains(output.String(), "Evaluated 1 seasons") {
		t.Fatalf("output=%q\njson=%s\nmarkdown=%s", output.String(), jsonValue, markdownValue)
	}
	if !strings.Contains(string(jsonValue), `"status": "incomplete"`) {
		t.Fatalf("expected incomplete diagnostic report, got %s", jsonValue)
	}
}

func TestRunFailsClosedWithoutHistoricalData(t *testing.T) {
	directory := t.TempDir()
	dbPath := filepath.Join(directory, "empty.sqlite")
	jsonPath, markdownPath := filepath.Join(directory, "report.json"), filepath.Join(directory, "report.md")
	err := run(context.Background(), []string{
		"-db", dbPath, "-seasons", "2025", "-development", "2024", "-held-out", "2025",
		"-iterations", "1", "-bootstrap-resamples", "1", "-json", jsonPath, "-markdown", markdownPath,
	}, dbPath, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "evaluation data is incomplete") {
		t.Fatalf("error = %v", err)
	}
	if _, err := os.Stat(jsonPath); !os.IsNotExist(err) {
		t.Fatalf("incomplete run wrote JSON report: %v", err)
	}
	if _, err := os.Stat(markdownPath); !os.IsNotExist(err) {
		t.Fatalf("incomplete run wrote Markdown report: %v", err)
	}
}

func TestCSVSetRejectsDuplicatesAndEmptyValues(t *testing.T) {
	if _, err := csvSet("2024,2024"); err == nil {
		t.Fatal("expected duplicate error")
	}
	if _, err := csvSet("2024,"); err == nil {
		t.Fatal("expected empty item error")
	}
	if got, err := csvSet("2024, 2025"); err != nil || !got["2025"] {
		t.Fatalf("set=%v err=%v", got, err)
	}
}

func TestDefaultEvaluationWindowsAlternateEligibleSeasons(t *testing.T) {
	development, err := csvSet(defaultDevelopmentSeasons)
	if err != nil {
		t.Fatal(err)
	}
	heldout, err := csvSet(defaultHeldoutSeasons)
	if err != nil {
		t.Fatal(err)
	}
	if len(development) != 4 || len(heldout) != 5 {
		t.Fatalf("window sizes = development %d, held-out %d; want 4 and 5", len(development), len(heldout))
	}
	for _, season := range []string{"2016", "2018", "2021", "2023", "2025"} {
		if !heldout[season] || development[season] {
			t.Fatalf("season %s is not assigned to the alternating held-out window", season)
		}
	}
	for _, season := range []string{"2017", "2019", "2022", "2024"} {
		if !development[season] || heldout[season] {
			t.Fatalf("season %s is not assigned to the alternating development window", season)
		}
	}
}
