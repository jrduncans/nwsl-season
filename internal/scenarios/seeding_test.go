package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/clinching"
)

func TestSeedingMatchesOfficialStandings(t *testing.T) {
	for _, test := range []string{"level", "uneven", "head-to-head", "wins-decide"} {
		t.Run(test, func(t *testing.T) {
			f := newSeasonFixture(4)
			switch test {
			case "level":
				f.played("A", "B", 1, 1)
				f.played("C", "D", 1, 1)
			case "uneven":
				f.played("A", "C", 4, 0)
				f.played("B", "D", 1, 0)
			case "head-to-head":
				f.played("A", "B", 0, 2)
				f.played("C", "D", 2, 0)
			case "wins-decide":
				f.played("A", "C", 1, 1)
				f.played("A", "D", 1, 1)
				f.played("A", "B", 1, 1)
				f.played("B", "C", 1, 0)
				f.played("B", "D", 0, 1)
			}
			f.upcoming("A", "D")
			f.upcoming("B", "C")
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			results, err := GenerateSeeding(ctx, f.teams, f.games, f.slate(), [][2]int{{1, 2}})
			if err != nil {
				t.Fatal(err)
			}
			f.checkSeeding(t, results, [][2]int{{1, 2}}, 9, true)
		})
	}
}

func TestIntegerScoreFeasibility(t *testing.T) {
	for _, test := range []struct {
		name string
		rows []scoreInequality
		want scoreFeasibility
	}{
		{"integer", []scoreInequality{{[]int64{2}, 4}, {[]int64{-2}, -4}}, scorePossible},
		{"fractional", []scoreInequality{{[]int64{2}, 3}, {[]int64{-2}, -3}}, scoreImpossible},
		{"contradiction", []scoreInequality{{[]int64{1}, 0}, {[]int64{-1}, -1}}, scoreImpossible},
	} {
		t.Run(test.name, func(t *testing.T) {
			budget := 128
			if got := integerScores(context.Background(), test.rows, 1, &budget); got != test.want {
				t.Fatalf("got %v want %v", got, test.want)
			}
		})
	}
}

func (f *seasonFixture) coversAny(clauses []Clause, scores [][2]int) bool {
	for _, c := range clauses {
		if f.covers(c, scores) {
			return true
		}
	}
	return false
}

// resultScores is one scoreline for each result assignment of the pending
// matches; result-only clauses cover it exactly when they cover the results.
func (f *seasonFixture) resultScores() [][][2]int {
	out := [][][2]int{{}}
	for range f.pending {
		next := [][][2]int{}
		for _, scores := range out {
			for _, score := range [][2]int{{1, 0}, {0, 0}, {0, 1}} {
				next = append(next, append(append([][2]int{}, scores...), score))
			}
		}
		out = next
	}
	return out
}

func resultsKey(scores [][2]int) string {
	key := ""
	for _, score := range scores {
		key += string(scoreOutcome(score[0], score[1]))
	}
	return key
}

// checkSeeding compares every scoreline with the official table: a seed's
// conditions must hold exactly when the team finishes there, and a listed
// opponent must be possible with exactly the listed results. checkSupport
// also requires every listed result assignment to be reachable within
// maxGoals, which only holds when maxGoals covers every deciding margin.
func (f *seasonFixture) checkSeeding(t *testing.T, results map[string]*SeedingResult, pairs [][2]int, maxGoals int, checkSupport bool) {
	t.Helper()
	partner := map[int]int{}
	for _, p := range pairs {
		partner[p[0]], partner[p[1]] = p[1], p[0]
	}
	places := 2 * len(pairs)
	reached := map[string]bool{}
	f.eachScoreline(maxGoals, func(scores [][2]int, place placement) {
		for team, result := range results {
			seed := place.best[team] + 1
			known := place.best[team] == place.worst[team]
			if seed > places {
				seed = 0
				known = place.best[team] >= places
			}
			opponent := ""
			if known && seed > 0 {
				for other, p := range place.best {
					if p+1 == partner[seed] && place.worst[other] == p {
						opponent = other
					}
				}
			}
			found := false
			for _, s := range result.Seeds {
				if !f.coversAny(s.Clauses, scores) {
					continue
				}
				found = true
				if !known || s.Seed != seed {
					t.Fatalf("%s %v: claimed seed %d; official %+v", team, scores, s.Seed, place)
				}
			}
			if known && !found && result.Limitation == "" {
				t.Fatalf("%s %v: missing seed %d", team, scores, seed)
			}
			if !known || (seed > 0 && opponent == "") {
				continue
			}
			reached[fmt.Sprintf("%s:%d:%s:%s", team, seed, opponent, resultsKey(scores))] = true
			listed := false
			for _, s := range result.Seeds {
				if s.Seed != seed {
					continue
				}
				if seed == 0 {
					listed = f.coversAny(s.PossibleResults, scores)
				}
				for _, o := range s.Opponents {
					listed = listed || (o.TeamID == opponent && f.coversAny(o.PossibleResults, scores))
				}
			}
			if !listed && result.Limitation == "" {
				t.Fatalf("%s %v: seed %d vs %s is not listed", team, scores, seed, opponent)
			}
		}
	})
	if !checkSupport {
		return
	}
	for team, result := range results {
		for _, s := range result.Seeds {
			type support struct {
				opponent string
				clauses  []Clause
			}
			supports := []support{{"", s.PossibleResults}}
			if s.Seed > 0 {
				supports = nil
				for _, o := range s.Opponents {
					supports = append(supports, support{o.TeamID, o.PossibleResults})
				}
			}
			for _, sup := range supports {
				for _, scores := range f.resultScores() {
					if f.coversAny(sup.clauses, scores) && !reached[fmt.Sprintf("%s:%d:%s:%s", team, s.Seed, sup.opponent, resultsKey(scores))] {
						t.Errorf("%s: seed %d vs %q listed for unreachable results %s", team, s.Seed, sup.opponent, resultsKey(scores))
					}
				}
			}
		}
	}
}

func TestSeedingFixedResultsPreserveExactCoverage(t *testing.T) {
	f := newSeasonFixture(4)
	f.played("A", "B", 1, 0)
	f.played("C", "D", 0, 1)
	f.upcoming("A", "C")
	f.upcoming("B", "D")
	results, err := GenerateSeeding(context.Background(), f.teams, f.games, f.slate(), [][2]int{{1, 2}})
	if err != nil {
		t.Fatal(err)
	}
	for _, home := range []clinching.Outcome{clinching.HomeWin, clinching.Draw, clinching.AwayWin} {
		fixed := map[string]clinching.Outcome{f.games[f.pending[0]].ID: home}
		for team, result := range results {
			before, _ := json.Marshal(result)
			filtered := FilterSeeding(*result, fixed)
			after, _ := json.Marshal(result)
			if string(before) != string(after) {
				t.Fatal("filter mutated cached result")
			}
			f.eachScoreline(6, func(scores [][2]int, _ placement) {
				if scoreOutcome(scores[0][0], scores[0][1]) != home {
					return
				}
				before := map[string]bool{}
				for _, seed := range result.Seeds {
					before[fmt.Sprint(seed.Seed)] = f.coversAny(seed.Clauses, scores)
					for _, o := range seed.Opponents {
						before[fmt.Sprint(seed.Seed, o.TeamID)] = f.coversAny(o.PossibleResults, scores)
					}
				}
				after := map[string]bool{}
				for _, seed := range filtered.Seeds {
					after[fmt.Sprint(seed.Seed)] = f.coversAny(seed.Clauses, scores)
					for _, o := range seed.Opponents {
						after[fmt.Sprint(seed.Seed, o.TeamID)] = f.coversAny(o.PossibleResults, scores)
					}
				}
				for key, want := range before {
					if after[key] != want {
						t.Fatalf("%s fixed %s %v changed %s", team, home, scores, key)
					}
				}
			})
			for _, o := range filtered.Seeds {
				clauses := append(append([]Clause{}, o.Clauses...), o.PossibleResults...)
				for _, opponent := range o.Opponents {
					clauses = append(clauses, opponent.PossibleResults...)
				}
				for _, c := range clauses {
					for _, condition := range c.Conditions {
						if _, ok := fixed[condition.GameID]; ok {
							t.Fatal("fixed match remained in conditions")
						}
					}
				}
			}
		}
	}
}

func TestSeedingRandomSmallLeagues(t *testing.T) {
	random := rand.New(rand.NewSource(205)) // #nosec G404 -- reproducible test leagues, not security randomness.
	for range 12 {
		f := newSeasonFixture(6)
		for i := range f.teams {
			for j := i + 1; j < len(f.teams); j++ {
				f.played(f.teams[i].ID, f.teams[j].ID, random.Intn(3), random.Intn(3))
			}
		}
		f.upcoming("A", "B")
		f.upcoming("C", "D")
		f.upcoming("E", "F")
		results, err := GenerateSeeding(context.Background(), f.teams, f.games, f.slate(), [][2]int{{1, 4}, {2, 3}})
		if err != nil {
			t.Fatal(err)
		}
		f.checkSeeding(t, results, [][2]int{{1, 4}, {2, 3}}, 2, false)
	}
}

func TestSeedingCancelledBatchIsRetryable(t *testing.T) {
	f := newSeasonFixture(4)
	f.upcoming("A", "B")
	f.upcoming("C", "D")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := GenerateSeeding(ctx, f.teams, f.games, f.slate(), [][2]int{{1, 2}})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range result {
		if r.Limitation != LimitationBudgetPartial {
			t.Fatalf("missing budget limitation: %+v", r)
		}
	}
}

func TestMissPlayoffsIncludesUnresolvedTieBelowCutoff(t *testing.T) {
	// Two other rivals already finish ahead. Whether this pair is split by
	// head-to-head goals or discipline cannot restore a top-two finish.
	s := seasonSearch{target: 0, minimum: true, teamIDs: []string{"A", "B", "C", "D"}, h2hPoints: make([][]int, 4), teamGames: make([][]int, 4)}
	for i := range s.h2hPoints {
		s.h2hPoints[i] = make([]int, 4)
	}
	levels := []levelRival{{rival: 1, h2hFromRemaining: true, h2hLow: -1, h2hHigh: 1}}
	set := s.settleLevels(levels, []int{stateLevel}, []pairResult{{verdict: pairBehind}, {verdict: pairBehind}}, 1)
	if len(set.alternatives) != 1 || len(set.alternatives[0]) != 0 || set.disciplinary || set.unresolved {
		t.Fatalf("miss should be unconditional: %+v", set)
	}
}

func TestSeedingDrawDrawCannotReverseWinsTiebreak(t *testing.T) {
	f := newSeasonFixture(4)
	f.played("A", "C", 1, 1)
	f.played("A", "D", 1, 1)
	f.played("A", "B", 1, 1)
	f.played("B", "C", 1, 0)
	f.played("B", "D", 0, 1)
	f.upcoming("A", "D")
	f.upcoming("B", "C")
	results, err := GenerateSeeding(context.Background(), f.teams, f.games, f.slate(), [][2]int{{1, 2}})
	if err != nil {
		t.Fatal(err)
	}
	fixed := map[string]clinching.Outcome{}
	for _, i := range f.pending {
		fixed[f.games[i].ID] = clinching.Draw
	}
	filtered := FilterSeeding(*results["A"], fixed)
	if len(filtered.Seeds) != 1 || filtered.Seeds[0].Seed != 0 {
		t.Fatalf("A cannot qualify when both draw: %+v", filtered.Seeds)
	}
}
