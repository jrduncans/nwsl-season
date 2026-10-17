package app

import (
	"fmt"
	"strings"
)

// Both match views summarize the same oriented, selected-basis match values.
// Expected totals require every match; coverage never masquerades as a full total.
type exploreMarginTile struct {
	Label, Record, RecordLabel, Rate, Goals, Coverage, XG, XGCoverage, Matches string
}

func exploreMarginTiles(matches []exploreMatchRecord, nonPenalty bool) []exploreMarginTile {
	var tiles []exploreMarginTile
	for _, venue := range []string{"All", "Home", "Away"} {
		n, wins, draws, losses, points, covered, xgCovered := 0, 0, 0, 0, 0.0, 0, 0
		goals, xg := [2]float64{}, [2]float64{}
		for _, match := range matches {
			if venue != "All" && venue != match.Venue {
				continue
			}
			n++
			p := match.Values[3].Actual
			points += p
			switch p {
			case 3:
				wins++
			case 1:
				draws++
			default:
				losses++
			}
			hasXG := match.Values[0].Expected != nil && match.Values[1].Expected != nil
			if !nonPenalty || hasXG {
				covered++
				for i := range goals {
					goals[i] += match.Values[i].Actual
				}
			}
			if hasXG {
				xgCovered++
				for i := range xg {
					xg[i] += *match.Values[i].Expected
				}
			}
		}
		tile := exploreMarginTile{Label: venue, Record: fmt.Sprintf("%d–%d–%d", wins, draws, losses), RecordLabel: fmt.Sprintf("%d wins, %d draws, %d losses", wins, draws, losses), Rate: "— pts/match", Goals: fmt.Sprintf("%.0f–%.0f goals", goals[0], goals[1]), XG: "xG incomplete", Matches: h2hMatchCount(n)}
		if n > 0 {
			tile.Rate = fmt.Sprintf("%.2f pts/match", points/float64(n))
		}
		if nonPenalty {
			tile.Goals = fmt.Sprintf("%.0f–%.0f np goals", goals[0], goals[1])
			if covered == 0 {
				tile.Goals = "Goals unavailable"
			}
			if covered < n {
				tile.Coverage = fmt.Sprintf("%d/%d with split", covered, n)
			}
		}
		if xgCovered == n && n > 0 {
			tile.XG = fmt.Sprintf("%.2f–%.2f xG", xg[0], xg[1])
		} else {
			tile.XGCoverage = fmt.Sprintf("xG %d/%d matches", xgCovered, n)
		}
		if nonPenalty {
			tile.XG = strings.ReplaceAll(tile.XG, "xG", "npxG")
			tile.XGCoverage = strings.ReplaceAll(tile.XGCoverage, "xG", "npxG")
		}
		if n == 0 {
			tile.XG = "No matches"
		}
		tiles = append(tiles, tile)
	}
	return tiles
}
