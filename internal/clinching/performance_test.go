package clinching

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

// generatedHalfwaySeason is an adversarial deterministic 16-team/240-fixture
// season with 119 completed matches. The checked-in JSON fixture below covers
// the actual July 2026 topology; this generator supplies a different stress
// shape for the cutoff solver.
func generatedHalfwaySeason() ([]standings.Team, []standings.Game, []string) {
	teams := make([]standings.Team, 16)
	rotation := make([]int, len(teams))
	for i := range teams {
		teams[i] = standings.Team{ID: fmt.Sprintf("team-%02d", i)}
		rotation[i] = i
	}
	games := make([]standings.Game, 0, 240)
	for half := 0; half < 2; half++ {
		rotation = rotation[:0]
		for i := range teams {
			rotation = append(rotation, i)
		}
		for round := 0; round < 15; round++ {
			for match := 0; match < 8; match++ {
				home, away := rotation[match], rotation[15-match]
				if half == 1 {
					home, away = away, home
				}
				games = append(games, standings.Game{ID: fmt.Sprintf("game-%03d", len(games)), Status: "PreMatch", HomeTeamID: teams[home].ID, AwayTeamID: teams[away].ID})
			}
			last := rotation[len(rotation)-1]
			copy(rotation[2:], rotation[1:len(rotation)-1])
			rotation[1] = last
		}
	}
	for i := 0; i < 119; i++ {
		home, away := (i*7)%4, (i*5+1)%4
		games[i].Status = standings.CompletedStatus
		games[i].HomeScore, games[i].AwayScore = &home, &away
	}
	order := []string{}
	for _, game := range games {
		if game.Status == "PreMatch" {
			order = append(order, game.ID)
		}
	}
	return teams, games, order
}

func benchmarkAchievements() []competition.Achievement {
	return []competition.Achievement{
		{ID: competition.AchievementPlayoffs, Label: "Playoffs", TopK: 8},
		{ID: competition.AchievementHomePlayoff, Label: "Home playoff", TopK: 4},
		{ID: competition.AchievementShield, Label: "Shield", TopK: 1},
	}
}

func BenchmarkHalfwayQualificationStatuses(b *testing.B) {
	teams, games, order := checkedInHalfwaySeason(b)
	achievements := benchmarkAchievements()
	var totals Diagnostics
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		evaluator, err := NewEvaluator(teams, games, order)
		if err != nil {
			b.Fatal(err)
		}
		for _, team := range teams {
			for _, achievement := range achievements {
				value, err := evaluator.EvaluateStatus(context.Background(), team.ID, achievement, nil)
				if err != nil {
					b.Fatal(err)
				}
				if value.Method == ProofComputeBudget {
					b.Fatalf("compute-budget status for %s/%s", team.ID, achievement.ID)
				}
			}
		}
		mergeBenchmarkDiagnostics(&totals, evaluator.Diagnostics())
	}
	reportBenchmarkDiagnostics(b, totals)
}

func BenchmarkHalfwayAllNoHelpPaths(b *testing.B) {
	teams, games, order := checkedInHalfwaySeason(b)
	benchmarkNoHelpPaths(b, teams, games, order)
}

func BenchmarkGeneratedHalfwayAllNoHelpPaths(b *testing.B) {
	teams, games, order := generatedHalfwaySeason()
	benchmarkNoHelpPaths(b, teams, games, order)
}

func benchmarkNoHelpPaths(b *testing.B, teams []standings.Team, games []standings.Game, order []string) {
	achievements := benchmarkAchievements()
	var totals Diagnostics
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		evaluator, err := NewEvaluator(teams, games, order)
		if err != nil {
			b.Fatal(err)
		}
		for _, team := range teams {
			bases := map[competition.AchievementID]AchievementResult{}
			active := []competition.Achievement{}
			for _, achievement := range achievements {
				value, err := evaluator.EvaluateStatus(context.Background(), team.ID, achievement, nil)
				if err != nil {
					b.Fatal(err)
				}
				if value.Status == NotClinched {
					bases[achievement.ID] = value
					active = append(active, achievement)
				}
			}
			paths, err := evaluator.EvaluateNoHelpBatch(context.Background(), team.ID, active, nil, bases)
			if err != nil {
				b.Fatal(err)
			}
			for achievement, path := range paths {
				if path.State == NoHelpUnresolved && path.Reason == "calculation budget exhausted" {
					b.Fatalf("compute-budget no-help path for %s/%s", team.ID, achievement)
				}
			}
		}
		mergeBenchmarkDiagnostics(&totals, evaluator.Diagnostics())
	}
	reportBenchmarkDiagnostics(b, totals)
}

func BenchmarkWashingtonNoHelpBoundary(b *testing.B) {
	teams, games, order := checkedInHalfwaySeason(b)
	achievement := benchmarkAchievements()[0]
	var totals Diagnostics
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		evaluator, err := NewEvaluator(teams, games, order)
		if err != nil {
			b.Fatal(err)
		}
		base, err := evaluator.EvaluateStatus(context.Background(), "aDQ0lzvQEv", achievement, nil)
		if err != nil {
			b.Fatal(err)
		}
		path, err := evaluator.EvaluateNoHelp(context.Background(), "aDQ0lzvQEv", achievement, nil, base)
		if err != nil {
			b.Fatal(err)
		}
		if path.State != NoHelpGuaranteed || len(path.FixtureIDs) != 10 {
			b.Fatalf("Washington playoff boundary = %+v, want ten wins", path)
		}
		mergeBenchmarkDiagnostics(&totals, evaluator.Diagnostics())
	}
	reportBenchmarkDiagnostics(b, totals)
}

func TestHalfwayQualificationDeterministicCeilings(t *testing.T) {
	teams, games, order := checkedInHalfwaySeason(t)
	achievements := benchmarkAchievements()
	run := func() Diagnostics {
		evaluator, err := NewEvaluator(teams, games, order)
		if err != nil {
			t.Fatal(err)
		}
		for _, team := range teams {
			for _, achievement := range achievements {
				if _, err := evaluator.EvaluateStatus(context.Background(), team.ID, achievement, nil); err != nil {
					t.Fatal(err)
				}
			}
		}
		return evaluator.Diagnostics()
	}
	allocations := testing.AllocsPerRun(3, func() { run() })
	if allocations > 5_000 {
		t.Fatalf("halfway qualification allocations = %.0f, ceiling is 5000", allocations)
	}
	diagnostics := run()
	if diagnostics.VisitedStates > 1_000 || diagnostics.SubsetProbes > 100 {
		t.Fatalf("halfway qualification state ceiling exceeded: %+v", diagnostics)
	}
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

func checkedInHalfwaySeason(t testing.TB) ([]standings.Team, []standings.Game, []string) {
	t.Helper()
	data, err := os.ReadFile("../../testdata/halfway-2026.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture halfwayFixtureFile
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Snapshot != "2026-halfway-119-of-240" || len(fixture.Teams) != 16 || len(fixture.Games) != 240 {
		t.Fatalf("invalid halfway fixture metadata: %q, %d teams, %d games", fixture.Snapshot, len(fixture.Teams), len(fixture.Games))
	}
	teams := make([]standings.Team, len(fixture.Teams))
	for i, id := range fixture.Teams {
		teams[i] = standings.Team{ID: id}
	}
	type pendingGame struct {
		id      string
		kickoff time.Time
	}
	games := make([]standings.Game, len(fixture.Games))
	pending := []pendingGame{}
	completed := 0
	for i, game := range fixture.Games {
		games[i] = standings.Game{ID: game.ID, Status: game.Status, HomeTeamID: game.Home, AwayTeamID: game.Away, HomeScore: game.HomeScore, AwayScore: game.AwayScore}
		if game.Status == standings.CompletedStatus {
			completed++
			continue
		}
		kickoff, err := fixtures.ParseKickoff(game.KickoffUTC)
		if err != nil {
			t.Fatal(err)
		}
		pending = append(pending, pendingGame{id: game.ID, kickoff: kickoff})
	}
	if completed != 119 {
		t.Fatalf("halfway fixture has %d completed games, want 119", completed)
	}
	sort.Slice(pending, func(i, j int) bool {
		if pending[i].kickoff.Equal(pending[j].kickoff) {
			return pending[i].id < pending[j].id
		}
		return pending[i].kickoff.Before(pending[j].kickoff)
	})
	order := make([]string, len(pending))
	for i, game := range pending {
		order[i] = game.id
	}
	return teams, games, order
}

func mergeBenchmarkDiagnostics(total *Diagnostics, value Diagnostics) {
	total.SubsetProbes += value.SubsetProbes
	total.VisitedStates += value.VisitedStates
	total.IndividualPrunes += value.IndividualPrunes
	total.ComponentPrunes += value.ComponentPrunes
	total.TotalPrunes += value.TotalPrunes
}

func reportBenchmarkDiagnostics(b *testing.B, total Diagnostics) {
	b.ReportMetric(float64(total.SubsetProbes)/float64(b.N), "subset_probes/op")
	b.ReportMetric(float64(total.VisitedStates)/float64(b.N), "dp_states/op")
	b.ReportMetric(float64(total.IndividualPrunes)/float64(b.N), "individual_prunes/op")
	b.ReportMetric(float64(total.ComponentPrunes)/float64(b.N), "component_prunes/op")
	b.ReportMetric(float64(total.TotalPrunes)/float64(b.N), "total_prunes/op")
}
