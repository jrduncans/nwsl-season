package history

import (
	"fmt"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

func TestHeadToHeadOfficialRecordSanity(t *testing.T) {
	t.Parallel()
	// #151's Portland–Seattle sanity table: 13 meetings at either venue.
	scores := [][2]int64{{3, 0}, {3, 0}, {2, 0}, {2, 0}, {2, 0}, {2, 0}, {2, 2}, {2, 2}, {2, 2}, {2, 2}, {0, 1}, {0, 1}, {0, 2}, {2, 0}, {1, 0}, {1, 1}, {1, 1}, {1, 1}, {1, 2}, {1, 2}, {1, 2}, {0, 2}, {0, 2}, {0, 2}, {0, 2}, {0, 1}}
	var games []cache.Game
	for i, score := range scores {
		game := testGame(fmt.Sprintf("meeting-%02d", i), fixtures.CompletedStatus, score[0], score[1])
		game.KickoffUTC = fmt.Sprintf("2026-05-%02dT20:00:00Z", i+1)
		if i >= 13 {
			game.HomeTeamID, game.AwayTeamID = "away", "home"
			game.HomeScore.Int64, game.AwayScore.Int64 = score[1], score[0]
		}
		games = append(games, game)
	}
	input := h2hTestSeason("2026", games, nil, availableReadiness(cache.SourceScopeActive, cache.InventoryCompletenessUnknown))
	summary, err := SummarizeHeadToHead([]cache.HistoricalSeason{input}, "home", "away", AllShots)
	if err != nil {
		t.Fatal(err)
	}
	if summary.All != (HeadToHeadRecord{26, 8, 7, 11, 31, 31, 30}) || summary.Home != (HeadToHeadRecord{13, 6, 4, 3, 22, 22, 12}) || summary.Away != (HeadToHeadRecord{13, 2, 3, 8, 9, 9, 18}) {
		t.Fatalf("sanity record: %+v", summary)
	}
	if len(summary.Meetings) != 26 || !summary.Meetings[0].Active {
		t.Fatal("active season facts must be retained")
	}
	reverse, err := SummarizeHeadToHead([]cache.HistoricalSeason{input}, "away", "home", AllShots)
	if err != nil {
		t.Fatal(err)
	}
	if reverse.All.Wins != summary.All.Losses || reverse.All.GoalsFor != summary.All.GoalsAgainst || reverse.Home.Played != summary.Away.Played {
		t.Fatal("reversed orientation")
	}
}

func TestHeadToHeadGapsOrderingAndOpponents(t *testing.T) {
	t.Parallel()
	var inputs []cache.HistoricalSeason
	for _, year := range []string{"2018", "2019", "2024", "2026"} {
		game := testGame(year, fixtures.CompletedStatus, 0, 0)
		game.KickoffUTC = year + "-06-01T01:00:00Z"
		input := h2hTestSeason(year, []cache.Game{game}, nil, availableReadiness(cache.SourceScopeActive, cache.InventoryCompletenessUnknown))
		input.Data.Teams = []standings.Team{{ID: "home", Name: "Utah Royals"}, {ID: "away", Name: "Old opponent"}}
		if year == "2026" {
			input.Data.Teams[1].Name = "Newest opponent"
			input.Data.Games[0].KickoffUTC = ""
		}
		inputs = append([]cache.HistoricalSeason{input}, inputs...)
	}
	summary, err := SummarizeHeadToHead(inputs, "home", "away", AllShots)
	if err != nil {
		t.Fatal(err)
	}
	if summary.All.Played != 4 || !summary.Undated || summary.Meetings[0].Season != "2018" || summary.Meetings[3].Season != "2026" || summary.Meetings[0].OpponentName != "Old opponent" {
		t.Fatalf("gap/name/undated: %+v", summary)
	}
	opponents, err := HeadToHeadOpponents(inputs, "home")
	if err != nil {
		t.Fatal(err)
	}
	if len(opponents) != 1 || opponents[0] != (HeadToHeadOpponent{"away", "Newest opponent", 4}) {
		t.Fatalf("opponents: %+v", opponents)
	}
	empty, err := SummarizeHeadToHead(inputs, "home", "distinct-ASA-ID", AllShots)
	if err != nil || empty.All.Played != 0 {
		t.Fatal("no franchise inference")
	}
}

func TestHeadToHeadOneMeetingAndIntegrity(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		games []cache.Game
		want  int
	}{
		{"one", []cache.Game{testGame("one", fixtures.CompletedStatus, 1, 0)}, 1},
		{"abandoned excluded", []cache.Game{testGame("one", fixtures.CompletedStatus, 1, 0), testGame("abandoned", fixtures.AbandonedStatus, 9, 9)}, 1},
		{"malformed season excluded", []cache.Game{testGame("one", fixtures.CompletedStatus, 1, 0), testGame("malformed", fixtures.CompletedStatus, -1, 0)}, 0},
		{"no meetings", []cache.Game{testGame("pending", fixtures.PreMatchStatus, 0, 0)}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := h2hTestSeason("2026", tc.games, nil, availableReadiness(cache.SourceScopeActive, cache.InventoryCompletenessUnknown))
			summary, err := SummarizeHeadToHead([]cache.HistoricalSeason{input}, "home", "away", AllShots)
			if err != nil || summary.All.Played != tc.want {
				t.Fatalf("got %+v, %v", summary, err)
			}
		})
	}
	a := testGame("b", fixtures.CompletedStatus, 1, 0)
	a.KickoffUTC = "2026-05-01T21:00:00+01:00"
	b := testGame("a", fixtures.CompletedStatus, 1, 0)
	b.KickoffUTC = "2026-05-01T20:00:00Z"
	c := testGame("c", fixtures.CompletedStatus, 1, 0)
	c.KickoffUTC = "2026-05-01T19:30:00Z"
	input := h2hTestSeason("2026", []cache.Game{a, b, c}, nil, availableReadiness(cache.SourceScopeActive, cache.InventoryCompletenessUnknown))
	summary, err := SummarizeHeadToHead([]cache.HistoricalSeason{input}, "home", "away", AllShots)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Meetings[0].ID != "c" || summary.Meetings[1].ID != "a" || summary.Meetings[2].ID != "b" {
		t.Fatal("instant order / fixture ID tie")
	}
}

func h2hTestSeason(year string, games []cache.Game, xg []cache.GameXG, readiness *cache.SeasonReadinessSnapshot) cache.HistoricalSeason {
	games = append([]cache.Game(nil), games...)
	for i := range games {
		games[i].Season = year
	}
	return testSeason(year, games, xg, readiness)
}

func TestHeadToHeadExpectedCoverage(t *testing.T) {
	t.Parallel()
	games := []cache.Game{testGame("a", fixtures.CompletedStatus, 2, 1), testGame("b", fixtures.CompletedStatus, 0, 0)}
	for _, tc := range []struct {
		name                     string
		missingXG, missingPoints bool
	}{
		{"complete", false, false}, {"only xG missing", true, false}, {"only xPoints missing", false, true}, {"mixed", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			xg := []cache.GameXG{testXG(games[0], 1.5, .5, true, 2.1, .4), testXG(games[1], 0, 0, true, 1, 1)}
			if tc.missingXG {
				xg[0].HomeXG.Valid = false
			}
			if tc.missingPoints {
				xg[1].HomeXPoints.Valid = false
			}
			input := h2hTestSeason("2026", games, xg, availableReadiness(cache.SourceScopeActive, cache.InventoryCompletenessUnknown))
			got, err := SummarizeHeadToHead([]cache.HistoricalSeason{input}, "home", "away", AllShots)
			if err != nil {
				t.Fatal(err)
			}
			e := got.AllExpected
			if got.All.Points != 4 || got.All.GoalsFor != 2 || e.GoalsCovered != 2 || e.GoalsFor != 2 || len(got.Meetings) != 2 {
				t.Fatalf("actual: %+v", got)
			}
			if (e.XGFor == nil) != tc.missingXG || (e.XPoints == nil) != tc.missingPoints {
				t.Fatalf("independent coverage: %+v", e)
			}
			if !tc.missingXG {
				assertFloat(t, e.XGFor, 1.5)
				assertFloat(t, e.XGAgainst, .5)
			}
			if !tc.missingPoints {
				assertFloat(t, e.XPoints, 3.1)
			}
			if got.Meetings[1].XGFor == nil || *got.Meetings[1].XGFor != 0 {
				t.Fatal("zero per-meeting xG must survive incomplete totals")
			}
		})
	}
}

func TestHeadToHeadNonPenaltyKeepsMeetingsAndPoints(t *testing.T) {
	t.Parallel()
	games := []cache.Game{testGame("a", fixtures.CompletedStatus, 2, 1), testGame("b", fixtures.CompletedStatus, 0, 0)}
	xg := []cache.GameXG{withSplit(testXG(games[0], 1.5, .5, true, 2.1, .4), cache.PenaltySplit{HomeGoals: 1, HomeXG: .76, HomeShots: 1}), testXG(games[1], 0, 0, true, 1, 1)}
	input := h2hTestSeason("2026", games, xg, availableReadiness(cache.SourceScopeActive, cache.InventoryCompletenessUnknown))
	archive := []cache.HistoricalSeason{input}
	all, err := SummarizeHeadToHead(archive, "home", "away", AllShots)
	if err != nil {
		t.Fatal(err)
	}
	np, err := SummarizeHeadToHead(archive, "home", "away", NonPenalty)
	if err != nil {
		t.Fatal(err)
	}
	if all.All != np.All || len(np.Meetings) != len(all.Meetings) {
		t.Fatal("basis changed official record or meeting list")
	}
	for i, m := range np.Meetings {
		if m.ID != all.Meetings[i].ID || m.Points != all.Meetings[i].Points {
			t.Fatal("basis changed meeting")
		}
	}
	if np.AllExpected.GoalsCovered != 1 || np.AllExpected.XGCovered != 1 || np.AllExpected.GoalsFor != 1 || np.AllExpected.GoalsAgainst != 1 || np.Meetings[1].GoalsCovered || np.Meetings[1].XGFor != nil {
		t.Fatalf("split coverage: %+v", np)
	}
	assertFloat(t, np.AllExpected.XGFor, .74)
	assertFloat(t, np.AllExpected.XPoints, 3.1)
	reverse, err := SummarizeHeadToHead(archive, "away", "home", NonPenalty)
	if err != nil {
		t.Fatal(err)
	}
	assertFloat(t, reverse.AllExpected.XGFor, .5)
	assertFloat(t, reverse.AllExpected.XGAgainst, .74)
	assertFloat(t, reverse.AllExpected.XPoints, 1.4)
	// With both splits the comparison covers both meetings; no global gate.
	xg[1].Penalties = &cache.PenaltySplit{}
	input.Data.XGoals = xg
	complete, err := SummarizeHeadToHead([]cache.HistoricalSeason{input}, "home", "away", NonPenalty)
	if err != nil || complete.AllExpected.GoalsCovered != 2 {
		t.Fatalf("complete split: %+v %v", complete, err)
	}
}
