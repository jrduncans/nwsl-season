package scenarios

import (
	"context"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/clinching"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

func TestForcedPointsEliminationMatchesExhaustiveCompletions(t *testing.T) {
	// #nosec G404 -- fixed-seed randomness only varies non-security test fixtures.
	rng := rand.New(rand.NewSource(26))
	teams := []string{"target", "a", "b", "c", "d"}
	for trial := range 300 {
		points := map[string]int{}
		for _, team := range teams {
			points[team] = rng.Intn(10)
		}
		games := make([]standings.Game, 3)
		for i := range games {
			home := rng.Intn(len(teams))
			away := rng.Intn(len(teams) - 1)
			if away >= home {
				away++
			}
			games[i] = standings.Game{ID: string(rune('a' + i)), HomeTeamID: teams[home], AwayTeamID: teams[away]}
		}
		topK := 1 + rng.Intn(len(teams)-1)
		got, err := forcedPointsElimination(context.Background(), points, teams, "target", topK, games[:1], games[1:])
		if err != nil {
			t.Fatal(err)
		}
		want := bruteForceElimination(points, teams, "target", topK, games)
		if got != want {
			t.Fatalf("trial %d: points=%v games=%v topK=%d: got forced=%t, want %t", trial, points, games, topK, got, want)
		}
	}
}

func bruteForceElimination(points map[string]int, teams []string, target string, topK int, games []standings.Game) bool {
	ceiling := points[target]
	for _, game := range games {
		if game.HomeTeamID == target || game.AwayTeamID == target {
			ceiling += 3
		}
	}
	var survives func(int, map[string]int) bool
	survives = func(index int, current map[string]int) bool {
		if index == len(games) {
			ahead := 0
			for _, team := range teams {
				if team != target && current[team] > ceiling {
					ahead++
				}
			}
			return ahead < topK
		}
		game := games[index]
		if game.HomeTeamID == target || game.AwayTeamID == target {
			return survives(index+1, current)
		}
		for _, award := range [][2]int{{3, 0}, {1, 1}, {0, 3}} {
			current[game.HomeTeamID] += award[0]
			current[game.AwayTeamID] += award[1]
			possible := survives(index+1, current)
			current[game.HomeTeamID] -= award[0]
			current[game.AwayTeamID] -= award[1]
			if possible {
				return true
			}
		}
		return false
	}
	current := map[string]int{}
	for team, value := range points {
		current[team] = value
	}
	return !survives(0, current)
}

func TestGenerateFindsEliminationForcedByLaterFixture(t *testing.T) {
	teams := []standings.Team{}
	for _, id := range []string{"target", "a", "b", "c", "e", "f", "g", "h"} {
		teams = append(teams, standings.Team{ID: id})
	}
	zero, one := 0, 1
	completed := func(id, home, away string, homeScore, awayScore *int) standings.Game {
		return standings.Game{ID: id, Status: standings.CompletedStatus, HomeTeamID: home, AwayTeamID: away, HomeScore: homeScore, AwayScore: awayScore}
	}
	games := []standings.Game{
		completed("t-win", "target", "e", &one, &zero),
		completed("t-draw", "target", "f", &one, &one),
		completed("a-win", "a", "f", &one, &zero),
		completed("a-draw-1", "a", "g", &one, &one),
		completed("a-draw-2", "a", "h", &one, &one),
		completed("b-win", "b", "g", &one, &zero),
		completed("c-draw-1", "c", "f", &one, &one),
		completed("c-draw-2", "c", "h", &one, &one),
		{ID: "slate", Status: "PreMatch", HomeTeamID: "b", AwayTeamID: "e"},
		{ID: "later", Status: "PreMatch", HomeTeamID: "b", AwayTeamID: "c"},
	}
	start := time.Date(2026, 9, 25, 20, 0, 0, 0, time.UTC)
	slate, err := DefineSlate([]ScheduledGame{
		{ID: "slate", Status: "PreMatch", HomeTeamID: "b", AwayTeamID: "e", KickoffUTC: start, Matchday: intPtr(1)},
		{ID: "later", Status: "PreMatch", HomeTeamID: "b", AwayTeamID: "c", KickoffUTC: start.Add(7 * 24 * time.Hour), Matchday: intPtr(2)},
	})
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err := clinching.NewEvaluator(teams, games, []string{"slate", "later"})
	if err != nil {
		t.Fatal(err)
	}
	achievement := competition.Achievement{ID: competition.AchievementPlayoffs, TopK: 2}
	baseline, err := evaluator.EvaluateStatus(context.Background(), "target", achievement, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Generate(context.Background(), Request{Evaluator: evaluator, Teams: teams, Games: games, Slate: slate, TargetTeamID: "target", Achievement: achievement, Baseline: baseline})
	if err != nil {
		t.Fatal(err)
	}
	if result.AlreadyEliminated || !result.CanBeEliminated || len(result.EliminationClauses) != 1 {
		t.Fatalf("elimination result = %+v, want one later-fixture-forced clause", result)
	}
	conditions := result.EliminationClauses[0].Conditions
	if len(conditions) != 1 || conditions[0].GameID != "slate" || len(conditions[0].AllowedOutcomes) != 2 || conditions[0].AllowedOutcomes[0] != clinching.HomeWin || conditions[0].AllowedOutcomes[1] != clinching.Draw {
		t.Fatalf("conditions = %+v, want b win or draw", conditions)
	}
}

// This public 2026-09-25 slate caught the old already-above-ceiling shortcut:
// Seattle-North Carolina and Denver-LA/North Carolina can force an eighth club
// across the cutoff after the displayed slate. The two fixed slate fixtures
// cannot change Bay or Boston's threshold: NJY/SD are already above it and
// CHI/LOU cannot get above it even by winning out.
func TestSeptember2026EliminationIncludesFutureFixtureCoupling(t *testing.T) {
	points := map[string]int{
		"NJY": 51, "WAS": 46, "SD": 45, "UTA": 42,
		"POR": 42, "NC": 39, "LA": 39, "KC": 38,
		"SEA": 37, "DEN": 35, "ORL": 30, "HOU": 29,
		"BOS": 26, "BAY": 26, "LOU": 23, "CHI": 17,
	}
	teams := []string{"NJY", "WAS", "SD", "UTA", "POR", "NC", "LA", "KC", "SEA", "DEN", "ORL", "HOU", "BOS", "BAY", "LOU", "CHI"}
	slate := []standings.Game{
		{HomeTeamID: "LOU", AwayTeamID: "SD"},
		{HomeTeamID: "NJY", AwayTeamID: "CHI"},
		{HomeTeamID: "SEA", AwayTeamID: "BOS"},
		{HomeTeamID: "KC", AwayTeamID: "DEN"},
		{HomeTeamID: "WAS", AwayTeamID: "LA"},
		{HomeTeamID: "POR", AwayTeamID: "HOU"},
		{HomeTeamID: "BAY", AwayTeamID: "ORL"},
		{HomeTeamID: "UTA", AwayTeamID: "NC"},
	}
	laterPairs := strings.Fields(`ORL-SD SEA-NC LOU-UTA KC-BAY POR-BOS NJY-LA CHI-DEN HOU-WAS
		BOS-LOU WAS-NJY UTA-KC NC-SD DEN-LA HOU-ORL BAY-POR CHI-SEA
		CHI-HOU UTA-SEA POR-NC DEN-LOU LA-BAY ORL-NJY KC-WAS SD-BOS
		NC-DEN LA-BOS WAS-CHI BAY-SD SEA-ORL LOU-KC NJY-UTA HOU-POR`)
	later := make([]standings.Game, 0, len(laterPairs))
	for _, pair := range laterPairs {
		clubs := strings.Split(pair, "-")
		later = append(later, standings.Game{HomeTeamID: clubs[0], AwayTeamID: clubs[1]})
	}
	matched := map[string]int{}
	for sea := range 3 {
		for kc := range 3 {
			for was := range 3 {
				for por := range 3 {
					for bay := range 3 {
						for uta := range 3 {
							outcomes := []int{0, 0, sea, kc, was, por, bay, uta}
							current := make(map[string]int, len(points))
							for team, value := range points {
								current[team] = value
							}
							for i, game := range slate {
								current[game.HomeTeamID] += []int{3, 1, 0}[outcomes[i]]
								current[game.AwayTeamID] += []int{0, 1, 3}[outcomes[i]]
							}
							bostonExpected := sea == 0 || (sea == 1 && kc == 0 && was != 0)
							bayExpected := (bay == 2 && (sea == 0 || kc != 2)) ||
								(bay == 1 && sea == 0 && (kc == 0 || (kc == 2 && was != 0) || (kc == 2 && uta != 0) || (was != 0 && uta != 0))) ||
								(bay == 1 && kc == 0 && was != 0)
							for _, check := range []struct {
								team string
								want bool
							}{{"BOS", bostonExpected}, {"BAY", bayExpected}} {
								got, err := forcedPointsElimination(context.Background(), current, teams, check.team, 8, nil, later)
								if err != nil {
									t.Fatal(err)
								}
								if got != check.want {
									t.Fatalf("%s outcomes=%v: elimination=%t, want %t", check.team, outcomes, got, check.want)
								}
								if got {
									matched[check.team]++
								}
							}
						}
					}
				}
			}
		}
	}
	if matched["BOS"] != 297 || matched["BAY"] != 288 {
		t.Fatalf("matched elimination combinations = %v", matched)
	}
}
