package scenarios_test

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/clinching"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/scenarios"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

func BenchmarkScenarioSlateHalfwaySnapshot(b *testing.B) {
	teams, games, scheduled := loadBenchmarkSeason(b)
	slate, err := scenarios.DefineSlate(scheduled)
	if err != nil {
		b.Fatal(err)
	}
	order := make([]scenarios.ScheduledGame, 0)
	for _, game := range scheduled {
		if game.Status == fixtures.PreMatchStatus {
			order = append(order, game)
		}
	}
	sort.Slice(order, func(i, j int) bool { return order[i].KickoffUTC.Before(order[j].KickoffUTC) })
	fixtureOrder := make([]string, len(order))
	for i, game := range order {
		fixtureOrder[i] = game.ID
	}
	rules, _ := competition.ForSeason("2026", "Regular Season")
	var searchNodes, oracleCalls, opportunityPrunes, guaranteePrunes int
	var solver clinching.Diagnostics
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		evaluator, err := clinching.NewEvaluator(teams, games, fixtureOrder)
		if err != nil {
			b.Fatal(err)
		}
		for _, team := range teams {
			baselines := make(map[competition.AchievementID]clinching.AchievementResult, len(rules.Achievements))
			for _, achievement := range rules.Achievements {
				baseline, err := evaluator.EvaluateStatus(context.Background(), team.ID, achievement, nil)
				if err != nil {
					b.Fatal(err)
				}
				baselines[achievement.ID] = baseline
			}
			results, err := scenarios.GenerateBatch(context.Background(), scenarios.BatchRequest{Evaluator: evaluator, Teams: teams, Games: games, Slate: slate, TargetTeamID: team.ID, Achievements: rules.Achievements, Baselines: baselines})
			if err != nil {
				b.Fatal(err)
			}
			if len(results) != len(rules.Achievements) {
				b.Fatalf("scenario batch returned %d achievements, want %d", len(results), len(rules.Achievements))
			}
			for _, result := range results {
				if result.State == scenarios.OpportunityUnresolved && result.Limitation == "scenario computation budget exhausted" {
					b.Fatalf("compute-budget scenario for %s/%s", result.TeamID, result.Achievement)
				}
				searchNodes += result.Diagnostics.SearchNodes
				oracleCalls += result.Diagnostics.OracleCalls
				opportunityPrunes += result.Diagnostics.OpportunityPrunes
				guaranteePrunes += result.Diagnostics.GuaranteePrunes
			}
		}
		diagnostics := evaluator.Diagnostics()
		solver.SubsetProbes += diagnostics.SubsetProbes
		solver.VisitedStates += diagnostics.VisitedStates
		solver.IndividualPrunes += diagnostics.IndividualPrunes
		solver.ComponentPrunes += diagnostics.ComponentPrunes
		solver.TotalPrunes += diagnostics.TotalPrunes
	}
	b.ReportMetric(float64(searchNodes)/float64(b.N), "search_nodes/op")
	b.ReportMetric(float64(oracleCalls)/float64(b.N), "oracle_calls/op")
	b.ReportMetric(float64(opportunityPrunes)/float64(b.N), "opportunity_prunes/op")
	b.ReportMetric(float64(guaranteePrunes)/float64(b.N), "guarantee_prunes/op")
	b.ReportMetric(float64(solver.SubsetProbes)/float64(b.N), "subset_probes/op")
	b.ReportMetric(float64(solver.VisitedStates)/float64(b.N), "dp_states/op")
	b.ReportMetric(float64(solver.IndividualPrunes+solver.ComponentPrunes+solver.TotalPrunes)/float64(b.N), "solver_prunes/op")
}

type halfwayFixtureFile struct {
	Snapshot string               `json:"snapshot"`
	Teams    []string             `json:"teams"`
	Games    []halfwayFixtureGame `json:"games"`
}

type halfwayFixtureGame struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	Home       string `json:"home"`
	Away       string `json:"away"`
	HomeScore  *int   `json:"home_score"`
	AwayScore  *int   `json:"away_score"`
	KickoffUTC string `json:"kickoff_utc"`
	Matchday   int    `json:"matchday"`
}

func loadBenchmarkSeason(b *testing.B) ([]standings.Team, []standings.Game, []scenarios.ScheduledGame) {
	b.Helper()
	data, err := os.ReadFile("../../testdata/halfway-2026.json")
	if err != nil {
		b.Fatal(err)
	}
	var fixture halfwayFixtureFile
	if err := json.Unmarshal(data, &fixture); err != nil {
		b.Fatal(err)
	}
	if fixture.Snapshot != "2026-halfway-119-of-240" || len(fixture.Teams) != 16 || len(fixture.Games) != 240 {
		b.Fatalf("invalid halfway fixture")
	}
	teams := make([]standings.Team, len(fixture.Teams))
	for i, id := range fixture.Teams {
		teams[i] = standings.Team{ID: id}
	}
	games := make([]standings.Game, len(fixture.Games))
	scheduled := make([]scenarios.ScheduledGame, len(fixture.Games))
	completed := 0
	for i, value := range fixture.Games {
		game := standings.Game{ID: value.ID, Status: value.Status, HomeTeamID: value.Home, AwayTeamID: value.Away, HomeScore: value.HomeScore, AwayScore: value.AwayScore}
		kickoffTime, err := fixtures.ParseKickoff(value.KickoffUTC)
		if err != nil {
			b.Fatal(err)
		}
		matchday := value.Matchday
		games[i] = game
		scheduled[i] = scenarios.ScheduledGame{ID: value.ID, Status: value.Status, HomeTeamID: value.Home, AwayTeamID: value.Away, HomeScore: value.HomeScore, AwayScore: value.AwayScore, KickoffUTC: kickoffTime, Matchday: &matchday}
		if value.Status == standings.CompletedStatus {
			completed++
		}
	}
	if completed != 119 {
		b.Fatalf("halfway fixture has %d completed games, want 119", completed)
	}
	return teams, games, scheduled
}
