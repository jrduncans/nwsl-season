package competition

import (
	"testing"

	"github.com/jrduncans/nwsl-season/internal/standings"
)

func TestOfficialStandingsAwardsAndAlreadyCounted(t *testing.T) {
	overrides := HistoricalOutcomeOverrides()["2021"]
	reign, portland, spirit := overrides.Awards[0].Winner, overrides.Awards[1].Winner, overrides.Awards[0].Loser
	teams := []standings.Team{{ID: reign}, {ID: portland}, {ID: spirit}, {ID: "other"}}
	score := 0
	games := []standings.Game{}
	for _, pair := range [][2]string{{reign, portland}, {reign, "other"}, {portland, "other"}, {spirit, "other"}} {
		games = append(games, standings.Game{Status: standings.CompletedStatus, HomeTeamID: pair[0], AwayTeamID: pair[1], HomeScore: &score, AwayScore: &score})
	}
	// Other has three matches, Reign/Portland two, and Spirit one.
	check := func(games []standings.Game) {
		t.Helper()
		rows, err := OfficialStandings("2021", teams, games, standings.OrderOfficialTotal)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if row.Record.Played != 3 {
				t.Fatalf("%s played %d", row.Team.ID, row.Record.Played)
			}
			if row.Team.ID == spirit && (row.Record.Losses != 2 || row.Record.GoalsAgainst != 6 || row.Record.Points != 1) {
				t.Fatalf("Spirit record: %+v", row.Record)
			}
			if row.Team.ID == reign && (row.Record.Wins != 1 || row.Record.GoalsFor != 3 || row.Record.Points != 5) {
				t.Fatalf("Reign record: %+v", row.Record)
			}
		}
	}
	check(games)
	three := 3
	games = append(games, standings.Game{Status: standings.CompletedStatus, HomeTeamID: spirit, AwayTeamID: reign, HomeScore: &score, AwayScore: &three})
	check(games)
	games = append(games, standings.Game{Status: standings.CompletedStatus, HomeTeamID: portland, AwayTeamID: spirit, HomeScore: &three, AwayScore: &score})
	check(games)
}

func TestOfficialStandingsDeductsBeforeRankingWithoutMutatingGames(t *testing.T) {
	angel := HistoricalOutcomeOverrides()["2024"].Deductions[0].Team
	teams := []standings.Team{{ID: angel}, {ID: "other"}}
	three, zero := 3, 0
	games := []standings.Game{{Status: standings.CompletedStatus, HomeTeamID: angel, AwayTeamID: "other", HomeScore: &three, AwayScore: &zero}}
	rows, err := OfficialStandings("2024", teams, games, standings.OrderOfficialTotal)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Record.Points != 0 {
			t.Fatalf("points: %+v", row)
		}
	}
	raw := standings.Calculate(teams, games, standings.OfficialTotalRules())
	if raw[0].Record.Points != 3 {
		t.Fatal("cached match input changed")
	}
}

func TestOfficialStandingsReportsUnreconciledAwards(t *testing.T) {
	overrides := HistoricalOutcomeOverrides()["2021"]
	reign, spirit := overrides.Awards[0].Winner, overrides.Awards[0].Loser
	teams := []standings.Team{{ID: reign}, {ID: spirit}, {ID: "other"}}
	score := 0
	games := []standings.Game{}
	for range 3 {
		games = append(games, standings.Game{Status: standings.CompletedStatus, HomeTeamID: "other", AwayTeamID: spirit, HomeScore: &score, AwayScore: &score})
	}
	if _, err := OfficialStandings("2021", teams, games, standings.OrderOfficialTotal); err == nil {
		t.Fatal("expected a reconciliation error")
	}
}
