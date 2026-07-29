package forecast

import "github.com/jrduncans/nwsl-season/internal/standings"

// historyPool returns either every available earlier season (when limit is
// zero) or the requested number of most recently completed seasons. The
// legacy fields keep direct model tests and non-backtest callers compatible.
func historyPool(input FitInput, limit int) ([]standings.Game, map[string]ExpectedGoals) {
	seasons := input.HistoricalSeasons
	if len(seasons) == 0 {
		return append([]standings.Game(nil), input.HistoricalGames...), copyXGoals(input.HistoricalXGoals)
	}
	first := 0
	if limit > 0 && len(seasons) > limit {
		first = len(seasons) - limit
	}
	games := []standings.Game{}
	xgoals := map[string]ExpectedGoals{}
	for _, season := range seasons[first:] {
		games = append(games, season.Games...)
		for id, value := range season.XGoals {
			xgoals[id] = value
		}
	}
	return games, xgoals
}

func copyXGoals(source map[string]ExpectedGoals) map[string]ExpectedGoals {
	values := make(map[string]ExpectedGoals, len(source))
	for id, value := range source {
		values[id] = value
	}
	return values
}
