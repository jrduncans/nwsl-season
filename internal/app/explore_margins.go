package app

import (
	"fmt"

	"github.com/jrduncans/nwsl-season/internal/history"
)

// exploreMarginTile keeps arithmetic and coverage separate from display text.
// GoalsCovered comes from the scoring calculation, never inferred from xG.
type exploreMarginTile struct {
	Label        string  `json:"label"`
	Played       int     `json:"played"`
	Wins         int     `json:"wins"`
	Draws        int     `json:"draws"`
	Losses       int     `json:"losses"`
	Points       int     `json:"points"`
	GoalsCovered int     `json:"goalsCovered"`
	XGCovered    int     `json:"xgCovered"`
	GoalsFor     int64   `json:"goalsFor"`
	GoalsAgainst int64   `json:"goalsAgainst"`
	XGFor        float64 `json:"xgFor"`
	XGAgainst    float64 `json:"xgAgainst"`
}

type exploreMarginTileView struct {
	Label, Record, RecordLabel, Rate, Goals, Coverage, XG, XGCoverage, Matches string
}

func exploreMarginTiles(matches []exploreMatchRecord) []exploreMarginTile {
	tiles := []exploreMarginTile{{Label: "All"}, {Label: "Home"}, {Label: "Away"}}
	for i := range tiles {
		tile := &tiles[i]
		for _, match := range matches {
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
			if match.GoalsCovered {
				tile.GoalsCovered++
				tile.GoalsFor += int64(match.Values[exploreScored].Actual)
				tile.GoalsAgainst += int64(match.Values[exploreAllowed].Actual)
			}
			if match.Values[exploreScored].Expected != nil && match.Values[exploreAllowed].Expected != nil {
				tile.XGCovered++
				tile.XGFor += *match.Values[exploreScored].Expected
				tile.XGAgainst += *match.Values[exploreAllowed].Expected
			}
		}
	}
	return tiles
}

// This is the only Go formatting step; browser parity tests compare it with
// the in-place renderer for complete, missing-xG and missing-split populations.
func formatMarginTiles(tiles []exploreMarginTile, basis history.ScoringBasis) []exploreMarginTileView {
	views := make([]exploreMarginTileView, 0, len(tiles))
	for _, tile := range tiles {
		view := exploreMarginTileView{Label: tile.Label, Record: fmt.Sprintf("%d–%d–%d", tile.Wins, tile.Draws, tile.Losses), RecordLabel: fmt.Sprintf("%d wins, %d draws, %d losses", tile.Wins, tile.Draws, tile.Losses), Rate: "— pts/match", Goals: fmt.Sprintf("%d–%d goals", tile.GoalsFor, tile.GoalsAgainst), XG: basisText(basis, "xG incomplete"), Matches: exploreMatchCount(tile.Played)}
		if tile.Played > 0 {
			view.Rate = fmt.Sprintf("%.2f pts/match", float64(tile.Points)/float64(tile.Played))
		}
		if basis.ExcludesPenalties() {
			view.Goals = fmt.Sprintf("%d–%d np goals", tile.GoalsFor, tile.GoalsAgainst)
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
			view.XGCoverage = fmt.Sprintf("%s %d/%d matches", basisText(basis, "xG"), tile.XGCovered, tile.Played)
		}
		if tile.Played == 0 {
			view.XG = "No matches"
		}
		views = append(views, view)
	}
	return views
}

func exploreMatchCount(n int) string {
	if n == 1 {
		return "1 match"
	}
	return fmt.Sprintf("%d matches", n)
}
