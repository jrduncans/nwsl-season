package app

import (
	"fmt"
	"math"

	"github.com/jrduncans/nwsl-season/internal/history"
)

type exploreGoalGapTile struct {
	Label, Empty, Coverage, SplitCoverage string
	Sides                                 []exploreGoalGapSide
}

// Each side breaks into a heading, the totals and the gap, so a narrow tile
// wraps between those parts rather than inside them.
type exploreGoalGapSide struct {
	Heading, Value, Gap string
}

// Withhold incomplete totals rather than pairing different populations.
func exploreGoalGapTiles(matches []exploreMatchRecord, basis history.ScoringBasis, unit string) []exploreGoalGapTile {
	var views []exploreGoalGapTile
	for _, tile := range exploreMarginTiles(matches) {
		noun := unit + "s"
		if unit == "match" {
			noun = "matches"
		}
		if tile.Played == 0 {
			views = append(views, exploreGoalGapTile{Label: tile.Label, Empty: "No " + noun})
			continue
		}
		view := exploreGoalGapTile{Label: tile.Label}
		if tile.XGCovered < tile.Played {
			view.Coverage = fmt.Sprintf("%d of %d %s with %s", tile.XGCovered, tile.Played, noun, basisText(basis, "xG"))
		}
		// The value line and panel titles carry the basis, so headings stay short.
		headings := []string{"Scored", "Allowed"}
		actual := []int64{int64(tile.GoalsFor), int64(tile.GoalsAgainst)}
		expected := []float64{tile.XGFor, tile.XGAgainst}
		for i, heading := range headings {
			side := exploreGoalGapSide{Heading: heading}
			switch {
			case tile.GoalsCovered != tile.Played:
				side.Value = basisText(basis, "Goals incomplete")
			case tile.XGCovered != tile.Played:
				side.Value = fmt.Sprintf("%d", actual[i])
				side.Gap = basisText(basis, "xG incomplete")
			default:
				side.Value = fmt.Sprintf("%d vs %.2f %s", actual[i], expected[i], basisText(basis, "xG"))
				gap := float64(actual[i]) - expected[i]
				side.Gap = "matched expected"
				if math.Abs(gap) >= .005 {
					direction := "above"
					if gap < 0 {
						direction = "below"
					}
					side.Gap = fmt.Sprintf("%.2f %s", math.Abs(gap), direction)
				}
			}
			view.Sides = append(view.Sides, side)
		}
		if basis.ExcludesPenalties() && tile.GoalsCovered < tile.Played {
			view.SplitCoverage = fmt.Sprintf("%d of %d %s with penalty split", tile.GoalsCovered, tile.Played, noun)
		}
		views = append(views, view)
	}
	return views
}

func goalGap(match exploreMatchRecord, side int) *float64 {
	value := match.Values[side]
	if !match.GoalsCovered || value.Expected == nil {
		return nil
	}
	gap := value.Actual - *value.Expected
	return &gap
}

func goalGapText(match exploreMatchRecord, side int) string {
	if gap := goalGap(match, side); gap != nil {
		return h2hSignedMargin(*gap)
	}
	return "Unavailable"
}
