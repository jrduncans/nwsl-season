package asatest_test

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/asa"
	"github.com/jrduncans/nwsl-season/internal/asatest"
)

func penaltyFake(t *testing.T) *asatest.Server {
	t.Helper()
	fake := asatest.New(t)
	fake.SetGames([]asa.Game{
		game("g1", "a", "b", "2025", "2025-05-01 23:30:00 UTC", "FullTime"),
		game("g2", "b", "c", "2026", "2026-05-01 20:00:00 UTC", "FullTime"),
		game("g3", "c", "a", "2026", "2026-05-08 20:00:00 UTC", "FullTime"),
	})
	fake.SetStage("g3", "Playoffs")
	fake.SetXGoals([]asa.GameXGoals{
		{GameID: "g1", HomeTeamID: "a", AwayTeamID: "b", HomeTeamXGoals: 1},
		{GameID: "g2", HomeTeamID: "b", AwayTeamID: "c", HomeTeamXGoals: 2},
		{GameID: "g3", HomeTeamID: "c", AwayTeamID: "a", HomeTeamXGoals: 3},
	})
	fake.SetPenaltyXGoals([]asa.TeamGamePenaltyXGoals{
		{TeamID: "b", GameID: "g2", ShotsFor: 1, GoalsFor: 1, XGoalsFor: .76},
		{TeamID: "c", GameID: "g2", ShotsAgainst: 1, GoalsAgainst: 1, XGoalsAgainst: .76},
	})
	return fake
}

func TestPenaltyXGoalsServesZeroRowsAndExplicitRows(t *testing.T) {
	fake := penaltyFake(t)
	rows, err := fake.Client().GamePenaltyXGoals(context.Background(), asa.PenaltyXGoalsFilters{SeasonName: "2026", StageName: asatest.DefaultStage})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].GameID != "g2" || rows[0].XGoalsFor != .76 || rows[1].XGoalsAgainst != .76 {
		t.Fatalf("explicit rows = %+v", rows)
	}

	rows, err = fake.Client().GamePenaltyXGoals(context.Background(), asa.PenaltyXGoalsFilters{SeasonName: "2025"})
	if err != nil {
		t.Fatal(err)
	}
	zero := asa.TeamGamePenaltyXGoals{TeamID: "a", GameID: "g1", RawJSON: rows[0].RawJSON}
	if len(rows) != 2 || rows[0].TeamID != "a" || rows[1].TeamID != "b" || rows[0] != zero {
		t.Fatalf("zero rows = %+v", rows)
	}
}

func TestPenaltyXGoalsFiltersByStageAndUTCDate(t *testing.T) {
	fake := penaltyFake(t)
	games := func(filters asa.PenaltyXGoalsFilters) []string {
		t.Helper()
		rows, err := fake.Client().GamePenaltyXGoals(context.Background(), filters)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, row := range rows {
			if !slices.Contains(out, row.GameID) {
				out = append(out, row.GameID)
			}
		}
		return out
	}
	if got := games(asa.PenaltyXGoalsFilters{StageName: "Playoffs", SeasonName: "2026"}); !slices.Equal(got, []string{"g3"}) {
		t.Fatalf("stage = %v", got)
	}
	if got := games(asa.PenaltyXGoalsFilters{StartDate: "2025-05-01", EndDate: "2025-05-01"}); !slices.Equal(got, []string{"g1"}) {
		t.Fatalf("UTC date (late-evening kickoff) = %v", got)
	}
	if got := games(asa.PenaltyXGoalsFilters{StartDate: "2026-05-02", EndDate: "2026-05-09", StageName: asatest.DefaultStage}); len(got) != 0 {
		t.Fatalf("regular season in window = %v, want none", got)
	}
	if got := games(asa.PenaltyXGoalsFilters{StartDate: "2026-04-30", EndDate: "2026-05-02"}); !slices.Equal(got, []string{"g2"}) {
		t.Fatalf("window = %v", got)
	}
}

func TestPenaltyXGoalsRejectsGameIDLikeTheRealAPI(t *testing.T) {
	fake := penaltyFake(t)
	response, err := http.Get(fake.URL() + asatest.PathTeamXGoals + "?split_by_games=true&shot_pattern=Penalty&game_id=g2")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", response.StatusCode)
	}
}

func TestPenaltyXGoalsSupportsFailNext(t *testing.T) {
	fake := penaltyFake(t)
	fake.FailNext(asatest.PathTeamXGoals, http.StatusBadGateway, 1)
	if _, err := fake.Client().GamePenaltyXGoals(context.Background(), asa.PenaltyXGoalsFilters{SeasonName: "2026"}); err == nil {
		t.Fatal("expected injected failure")
	}
	if _, err := fake.Client().GamePenaltyXGoals(context.Background(), asa.PenaltyXGoalsFilters{SeasonName: "2026"}); err != nil {
		t.Fatalf("second request: %v", err)
	}
}

func TestXGoalsCarryTheGameKickoff(t *testing.T) {
	fake := penaltyFake(t)
	values, err := fake.Client().GameXGoals(context.Background(), asa.XGoalsFilters{GameID: "g2"})
	if err != nil || len(values) != 1 || values[0].DateTimeUTC != "2026-05-01 20:00:00 UTC" {
		t.Fatalf("values = %+v, %v", values, err)
	}
}
