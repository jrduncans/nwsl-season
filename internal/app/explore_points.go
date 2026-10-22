package app

import "fmt"

// Points tiles compare the same full population; incomplete xPoints never
// produce a partial total or a gap against all of the actual points.
type explorePointsTile struct {
	Label, Actual, Expected, Gap, Coverage string
}

func explorePointsTiles(matches []exploreMatchRecord, unit string) []explorePointsTile {
	return pointsTiles(matches, unit, "%.0f")
}

// pointsTiles formats totals with actualFormat: whole for official points,
// two decimals for adjusted ones.
func pointsTiles(matches []exploreMatchRecord, unit, actualFormat string) []explorePointsTile {
	plural := "matches"
	if unit == "meeting" {
		plural = "meetings"
	}
	var tiles []explorePointsTile
	for _, venue := range []string{"All", "Home", "Away"} {
		n, covered, actual, expected := 0, 0, 0.0, 0.0
		for _, match := range matches {
			if venue != "All" && match.Venue != venue {
				continue
			}
			n++
			actual += match.Values[3].Actual
			if value := match.Values[3].Expected; value != nil {
				covered++
				expected += *value
			}
		}
		// Coverage is stated only when it qualifies the totals.
		tile := explorePointsTile{Label: venue, Actual: fmt.Sprintf(actualFormat, actual), Expected: "xPoints incomplete", Gap: "xPoints incomplete"}
		switch {
		case n == 0:
			tile.Actual, tile.Expected, tile.Gap = "No "+plural, "", ""
		case covered == n:
			tile.Expected = fmt.Sprintf("%.2f xPoints", expected)
			gap, direction := actual-expected, "above"
			if gap < 0 {
				gap, direction = -gap, "below"
			}
			tile.Gap = fmt.Sprintf("%.2f %s expected", gap, direction)
		default:
			tile.Coverage = fmt.Sprintf("%d of %d %s", covered, n, plural)
		}
		tiles = append(tiles, tile)
	}
	return tiles
}

func pointsGap(value exploreTeamValues) *float64 {
	if value.Expected == nil {
		return nil
	}
	gap := value.Actual - *value.Expected
	return &gap
}

// exploreAdjustedPointsTiles totals opponent-adjusted points and xPoints in
// the same lines as the official tiles, so the switch never adds a line. The
// same shift moves both, so each gap matches the official one.
func exploreAdjustedPointsTiles(adjusted []exploreMatchRecord) []explorePointsTile {
	tiles := pointsTiles(adjusted, "match", "%.2f")
	for i := range tiles {
		for _, match := range adjusted {
			if (tiles[i].Label == "All" || match.Venue == tiles[i].Label) && !adjustedPointsCovered(match) {
				tiles[i] = explorePointsTile{Label: tiles[i].Label, Actual: "Points unavailable"}
				break
			}
		}
	}
	return tiles
}
