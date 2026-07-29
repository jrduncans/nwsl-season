package forecast

import (
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/standings"
)

func TestHistoryPoolSelectsMostRecentCompletedSeasons(t *testing.T) {
	start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	input := FitInput{HistoricalSeasons: []HistoricalSeason{
		{ID: "2018", Ended: start, Games: []standings.Game{{ID: "2018/game"}}},
		{ID: "2019", Ended: start.AddDate(1, 0, 0), Games: []standings.Game{{ID: "2019/game"}}},
		{ID: "2021", Ended: start.AddDate(2, 0, 0), Games: []standings.Game{{ID: "2021/game"}}},
	}}
	games, _ := historyPool(input, 2)
	if len(games) != 2 || games[0].ID != "2019/game" || games[1].ID != "2021/game" {
		t.Fatalf("two-season history = %+v, want 2019 and 2021", games)
	}
	all, _ := historyPool(input, 0)
	if len(all) != 3 {
		t.Fatalf("all-history games = %d, want 3", len(all))
	}
}
