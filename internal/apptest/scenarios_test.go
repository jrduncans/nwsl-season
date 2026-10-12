package apptest_test

import (
	"testing"

	"github.com/jrduncans/nwsl-season/internal/apptest"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/history"
)

func TestTeamHistoryKickoffsStayInSeason(t *testing.T) {
	t.Parallel()
	archive := apptest.Scenario(t, apptest.ScenarioTeamHistory)
	for _, season := range archive {
		for _, game := range season.Data.Games {
			kickoff, err := fixtures.ParseKickoff(game.KickoffUTC)
			if err != nil {
				t.Fatal(err)
			}
			if kickoff.Format("2006") != season.Entry.Season {
				t.Fatalf("season %s fixture %s has kickoff %s", season.Entry.Season, game.ASAID, game.KickoffUTC)
			}
		}
	}
	pair, err := history.SummarizeHeadToHead(archive, "Pk5LeeNqOW", "7vQ7BBzqD1", history.AllShots)
	if err != nil {
		t.Fatal(err)
	}
	if pair.All.Played != 26 || pair.Home.Played != 13 || pair.Away.Played != 13 {
		t.Fatalf("Portland–Seattle meetings changed: %+v", pair)
	}
	if pair.Meetings[0].Season != "2016" || pair.Meetings[len(pair.Meetings)-1].Season != "2026" {
		t.Fatal("chronological meetings must start in 2016 and end in 2026")
	}
}

func TestHeadToHeadScenarioHasMixedCoverage(t *testing.T) {
	t.Parallel()
	archive := apptest.Scenario(t, apptest.ScenarioTeams)
	pair, err := history.SummarizeHeadToHead(archive, "315VnJ759x", "4JMAk47qKg", history.AllShots)
	if err != nil {
		t.Fatal(err)
	}
	missingXG, missingPoints, complete := false, false, false
	for _, m := range pair.Meetings {
		missingXG = missingXG || m.XGFor == nil && m.XPoints != nil
		missingPoints = missingPoints || m.XGFor != nil && m.XPoints == nil
		complete = complete || m.XGFor != nil && m.XPoints != nil
	}
	if !missingXG || !missingPoints || !complete {
		t.Fatal("teams must seed a pairing with independent missing expected observations and complete meetings")
	}
}
