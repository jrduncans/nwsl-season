package app

import (
	"context"
	"database/sql"
	"slices"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/apptest"
	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/history"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

func TestHistoricalInventoryPolicyAcrossConsumers(t *testing.T) {
	ctx := context.Background()
	db, err := cache.Open(ctx, t.TempDir()+"/cache.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	apptest.Seed(t, db, apptest.ScenarioThresholds)
	archive, err := db.HistoricalRegularSeasons(ctx)
	if err != nil {
		t.Fatal(err)
	}
	accepted := 0
	for _, season := range archive {
		if season.Entry.Season >= "2026" {
			continue
		}
		accepted++
		if season.Readiness == nil || season.Readiness.Completeness != cache.InventoryCompletenessComplete {
			t.Fatalf("%s readiness: %+v", season.Entry.Season, season.Readiness)
		}
		phase := classifySeasonPhase(season.Data, season.Entry.Inventory)
		if phase.Phase != seasonPhaseComplete || !phase.FinalStandingsSafe {
			t.Fatalf("%s phase: %+v", season.Entry.Season, phase)
		}
		summaries, err := history.SummarizeScoring([]cache.HistoricalSeason{season})
		if err != nil || !summaries[0].PlotEligible {
			t.Fatalf("%s Explore: %+v, %v", season.Entry.Season, summaries, err)
		}
		if season.Entry.Season != "2021" {
			continue
		}
		if len(season.Data.Games) != 118 || summaries[0].Played != 118 || season.Readiness.ObservedGames != 118 {
			t.Fatal("awards changed source/played counts")
		}
		table, err := competition.OfficialStandings("2021", season.Data.Teams, standingsGames(season.Data.Games), standings.OrderOfficialTotal)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range table {
			if row.Record.Played != 24 {
				t.Fatalf("official record: %+v", row.Record)
			}
		}
		xg := []cache.GameXG{}
		for _, game := range season.Data.Games {
			xg = append(xg, cache.GameXG{GameID: game.ASAID, HomeTeamID: game.HomeTeamID, AwayTeamID: game.AwayTeamID, Availability: cache.XGAvailable, HomeXG: sql.NullFloat64{Float64: 1, Valid: true}, AwayXG: sql.NullFloat64{Valid: true}})
		}
		season.Data.XGoals = xg
		summaries, err = history.SummarizeScoring([]cache.HistoricalSeason{season})
		if err != nil || summaries[0].XGPerMatch == nil || *summaries[0].XGPerMatch != 1 || summaries[0].XGCovered != 118 {
			t.Fatal("awards changed xG denominator")
		}
		// A full response with both forfeits absent still passes source validation.
		games := slices.Clone(season.Data.Games)
		for i := range games {
			games[i].LastUpdatedUTC = games[i].KickoffUTC
		}
		now := time.Now().UTC()
		metadata := cache.FullRefreshMetadata{Trigger: cache.SourceTriggerCLI, StartedAt: now, FinishedAt: now}
		if _, err := db.ReplaceGameInventory(ctx, "2021", "Regular Season", games, season.Entry.Inventory, metadata); err != nil {
			t.Fatalf("full refresh: %v", err)
		}
		if _, err := db.ReplaceGameInventory(ctx, "2021", "Regular Season", games[1:], season.Entry.Inventory, metadata); err == nil {
			t.Fatal("full refresh accepted unrelated missing fixture")
		}
		stored, err := db.Season(ctx, "2021", "Regular Season")
		if err != nil || len(stored.Games) != 118 {
			t.Fatal("rejected response changed cache")
		}
		// A genuinely incomplete cached season is excluded by every consumer.
		season.Data.Games = games[1:]
		if classifySeasonPhase(season.Data, season.Entry.Inventory).Phase == seasonPhaseComplete {
			t.Fatal("partial archive marked final")
		}
		teams := []cache.Team{}
		for _, team := range season.Data.Teams {
			teams = append(teams, cache.Team{ASAID: team.ID, Name: team.Name, RawJSON: "{}"})
		}
		if _, err := db.ReplaceSeason(ctx, "2021", "Regular Season", teams, games[1:], now); err != nil {
			t.Fatal(err)
		}
	}
	if accepted != 9 {
		t.Fatalf("verified %d historical seasons", accepted)
	}
	archive, err = db.HistoricalRegularSeasons(ctx)
	if err != nil {
		t.Fatal(err)
	}
	summaries, err := history.SummarizeScoring(archive)
	if err != nil {
		t.Fatal(err)
	}
	for _, summary := range summaries {
		if summary.Season == "2021" && (summary.Inventory != cache.InventoryCompletenessIncomplete || summary.PlotEligible || !slices.Contains(summary.Exclusions, "inventory_incomplete")) {
			t.Fatalf("missing fixture allowed in Explore: %+v", summary)
		}
	}
	for _, season := range marginSeasons(archive) {
		if season.Season == "2021" {
			t.Fatal("missing fixture allowed in Seeding history")
		}
	}
}
