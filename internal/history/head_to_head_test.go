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
	summary, err := SummarizeHeadToHead([]cache.HistoricalSeason{input}, "home", "away")
	if err != nil {
		t.Fatal(err)
	}
	if summary.All != (HeadToHeadRecord{26, 8, 7, 11, 31, 31, 30}) || summary.Home != (HeadToHeadRecord{13, 6, 4, 3, 22, 22, 12}) || summary.Away != (HeadToHeadRecord{13, 2, 3, 8, 9, 9, 18}) {
		t.Fatalf("sanity record: %+v", summary)
	}
	if len(summary.Meetings) != 26 || !summary.Meetings[0].Active {
		t.Fatal("active season facts must be retained")
	}
	reverse, err := SummarizeHeadToHead([]cache.HistoricalSeason{input}, "away", "home")
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
	summary, err := SummarizeHeadToHead(inputs, "home", "away")
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
	empty, err := SummarizeHeadToHead(inputs, "home", "distinct-ASA-ID")
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
			summary, err := SummarizeHeadToHead([]cache.HistoricalSeason{input}, "home", "away")
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
	summary, err := SummarizeHeadToHead([]cache.HistoricalSeason{input}, "home", "away")
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
