package app

import (
	"fmt"

	"github.com/jrduncans/nwsl-season/internal/history"
)

// exploreMarginTile keeps arithmetic and coverage separate from display text.
// GoalsCovered comes from the scoring calculation, never inferred from xG.
// Adjusted tiles total opponent-adjusted goals and xG; the record and points
// stay official.
type exploreMarginTile struct {
	Adjusted     bool    `json:"adjusted,omitempty"`
	Label        string  `json:"label"`
	Played       int     `json:"played"`
	Wins         int     `json:"wins"`
	Draws        int     `json:"draws"`
	Losses       int     `json:"losses"`
	Points       int     `json:"points"`
	GoalsCovered int     `json:"goalsCovered"`
	XGCovered    int     `json:"xgCovered"`
	GoalsFor     float64 `json:"goalsFor"`
	GoalsAgainst float64 `json:"goalsAgainst"`
	XGFor        float64 `json:"xgFor"`
	XGAgainst    float64 `json:"xgAgainst"`
}

type exploreMarginTileView struct {
	Label, Record, RecordLabel, Rate, Goals, Coverage, XG, XGCoverage, Matches string
}

func exploreMarginTiles(matches []exploreMatchRecord) []exploreMarginTile {
	return marginTiles(matches, matches, false)
}

// exploreAdjustedMarginTiles totals adjusted goals and xG beside the official
// record and points.
func exploreAdjustedMarginTiles(official, adjusted []exploreMatchRecord) []exploreMarginTile {
	return marginTiles(official, adjusted, true)
}

func marginTiles(official, values []exploreMatchRecord, adjusted bool) []exploreMarginTile {
	tiles := []exploreMarginTile{{Label: "All", Adjusted: adjusted}, {Label: "Home", Adjusted: adjusted}, {Label: "Away", Adjusted: adjusted}}
	for i := range tiles {
		tile := &tiles[i]
		for index, match := range official {
			if tile.Label != "All" && tile.Label != match.Venue {
				continue
			}
			tile.Played++
			points := int(match.Values[explorePoints].Actual)
			tile.Points += points
			switch points {
			case 3:
				tile.Wins++
			case 1:
				tile.Draws++
			default:
				tile.Losses++
			}
			value := values[index]
			if value.GoalsCovered {
				tile.GoalsCovered++
				tile.GoalsFor += value.Values[exploreScored].Actual
				tile.GoalsAgainst += value.Values[exploreAllowed].Actual
			}
			if value.Values[exploreScored].Expected != nil && value.Values[exploreAllowed].Expected != nil {
				tile.XGCovered++
				tile.XGFor += *value.Values[exploreScored].Expected
				tile.XGAgainst += *value.Values[exploreAllowed].Expected
			}
		}
	}
	return tiles
}

// This is the only Go formatting step; browser parity tests compare it with
// the in-place renderer for complete, missing-xG and missing-split populations.
// unit is "match" or "meeting", so Head to head counts meetings.
func formatMarginTiles(tiles []exploreMarginTile, basis history.ScoringBasis, unit string) []exploreMarginTileView {
	noun := "matches"
	if unit == "meeting" {
		noun = "meetings"
	}
	views := make([]exploreMarginTileView, 0, len(tiles))
	for _, tile := range tiles {
		// Official totals are whole goals; adjusted totals are not. Adjusted
		// lines keep the official wording, which the status line and panel
		// titles qualify, so toggling the switch never wraps a line.
		totals := fmt.Sprintf("%d–%d", int64(tile.GoalsFor), int64(tile.GoalsAgainst))
		if tile.Adjusted {
			totals = fmt.Sprintf("%.2f–%.2f", tile.GoalsFor, tile.GoalsAgainst)
		}
		view := exploreMarginTileView{Label: tile.Label, Record: fmt.Sprintf("%d–%d–%d", tile.Wins, tile.Draws, tile.Losses), RecordLabel: fmt.Sprintf("%d wins, %d draws, %d losses", tile.Wins, tile.Draws, tile.Losses), Rate: "— pts/match", Goals: totals + " goals", XG: basisText(basis, "xG incomplete"), Matches: exploreCount(tile.Played, unit, noun)}
		if tile.Played > 0 {
			view.Rate = fmt.Sprintf("%.2f pts/match", float64(tile.Points)/float64(tile.Played))
		}
		if basis.ExcludesPenalties() {
			view.Goals = totals + " np goals"
			if tile.GoalsCovered == 0 {
				view.Goals = "Goals unavailable"
			}
			if tile.GoalsCovered < tile.Played {
				view.Coverage = fmt.Sprintf("%d/%d with split", tile.GoalsCovered, tile.Played)
			}
		}
		if tile.XGCovered == tile.Played && tile.Played > 0 {
			view.XG = fmt.Sprintf("%.2f–%.2f %s", tile.XGFor, tile.XGAgainst, basisText(basis, "xG"))
		} else {
			view.XGCoverage = fmt.Sprintf("%s %d/%d %s", basisText(basis, "xG"), tile.XGCovered, tile.Played, noun)
		}
		if tile.Played == 0 {
			view.XG = "No " + noun
		}
		views = append(views, view)
	}
	return views
}

func exploreCount(n int, unit, plural string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %s", n, plural)
}
