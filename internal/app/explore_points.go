package app

import "fmt"

// Points tiles compare the same full population; incomplete xPoints never
// produce a partial total or a gap against all of the actual points.
type explorePointsTile struct {
	Label, Actual, Expected, Gap, Rate, Coverage string
}

func explorePointsTiles(matches []exploreMatchRecord, unit string) []explorePointsTile {
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
		tile := explorePointsTile{Label: venue, Actual: fmt.Sprintf("%.0f", actual), Expected: "xPts incomplete", Gap: "xPts incomplete", Rate: "Unavailable", Coverage: fmt.Sprintf("%d of %d %s", covered, n, plural)}
		if n == 0 {
			tile.Actual, tile.Expected, tile.Gap, tile.Rate = "No "+plural, "", "", ""
		}
		if n > 0 {
			tile.Rate = fmt.Sprintf("%.2f pts per %s", actual/float64(n), unit)
			if covered == n {
				tile.Expected = fmt.Sprintf("%.2f xPts", expected)
				gap, direction := actual-expected, "above"
				if gap < 0 {
					gap, direction = -gap, "below"
				}
				tile.Gap = fmt.Sprintf("%.2f %s expected", gap, direction)
				tile.Rate = fmt.Sprintf("%.2f pts vs %.2f xPts per %s", actual/float64(n), expected/float64(n), unit)
			}
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
