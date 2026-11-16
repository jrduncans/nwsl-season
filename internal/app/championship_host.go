package app

import (
	"encoding/json"

	"github.com/jrduncans/nwsl-season/internal/cache"
)

// gameStadium reads the ASA stadium ID from a cached game's source payload,
// or "" when the payload has none.
func gameStadium(game cache.Game) string {
	var venue struct {
		StadiumID string `json:"stadium_id"`
	}
	if json.Unmarshal([]byte(game.RawJSON), &venue) != nil {
		return ""
	}
	return venue.StadiumID
}

// championshipHost returns the finalist playing the designated-site final at
// its own usual stadium, or "" when the final is neutral or its venue is
// unknown. A team's usual stadium is the one it used most for regular-season
// home matches; a tie for most leaves it unknown. ASA has no neutral-site
// flag, so this comparison is the only venue evidence.
func championshipHost(regular []cache.Game, final cache.Game) string {
	stadium := gameStadium(final)
	if stadium == "" || final.HomeTeamID == "" || final.AwayTeamID == "" {
		return ""
	}
	usual := map[string]map[string]int{final.HomeTeamID: {}, final.AwayTeamID: {}}
	for _, game := range regular {
		if counts, ok := usual[game.HomeTeamID]; ok {
			if id := gameStadium(game); id != "" {
				counts[id]++
			}
		}
	}
	host := ""
	for _, team := range []string{final.HomeTeamID, final.AwayTeamID} {
		best, most, tied := "", 0, false
		for id, count := range usual[team] {
			switch {
			case count > most:
				best, most, tied = id, count, false
			case count == most:
				tied = true
			}
		}
		if !tied && best == stadium {
			if host != "" {
				return "" // Two finalists sharing a stadium have no venue edge.
			}
			host = team
		}
	}
	return host
}
