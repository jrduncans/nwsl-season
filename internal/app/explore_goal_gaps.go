package app

import (
	"fmt"

	"github.com/jrduncans/nwsl-season/internal/history"
)

type exploreGoalGapTile struct {
	Label, Scored, Allowed, Coverage, SplitCoverage string
}

// Withhold incomplete totals rather than pairing different populations.
func exploreGoalGapTiles(matches []exploreMatchRecord, basis history.ScoringBasis, unit string) []exploreGoalGapTile {
	var views []exploreGoalGapTile
	for _, tile := range exploreMarginTiles(matches) {
		noun := unit + "s"
		if unit == "match" {
			noun = "matches"
		}
		view := exploreGoalGapTile{Label: tile.Label, Coverage: fmt.Sprintf("%d of %d %s with %s", tile.XGCovered, tile.Played, noun, basisText(basis, "xG"))}
		labels := []string{"goals", "allowed"}
		if basis.ExcludesPenalties() {
			labels = []string{"non-penalty goals", "non-penalty allowed"}
		}
		actual := []int64{tile.GoalsFor, tile.GoalsAgainst}
		expected := []float64{tile.XGFor, tile.XGAgainst}
		lines := [2]string{}
		for i := range lines {
			switch {
			case tile.Played == 0:
				lines[i] = "No " + noun
			case tile.GoalsCovered != tile.Played:
				lines[i] = basisText(basis, "Goals incomplete")
			case tile.XGCovered != tile.Played:
				lines[i] = fmt.Sprintf("%d %s · %s", actual[i], labels[i], basisText(basis, "xG incomplete"))
			default:
				xg := basisText(basis, "xG")
				if i == 1 {
					xg += " allowed"
				}
				gap := float64(actual[i]) - expected[i]
				direction := "above"
				if gap < 0 {
					direction = "below"
					gap = -gap
				}
				lines[i] = fmt.Sprintf("%d %s vs %.2f %s · %.2f %s", actual[i], labels[i], expected[i], xg, gap, direction)
			}
		}
		view.Scored, view.Allowed = lines[0], lines[1]
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
