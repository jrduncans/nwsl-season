package app

import (
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/history"
)

type exploreThresholdView struct {
	ThresholdRows       []exploreThresholdRow
	ThresholdTableRows  []exploreThresholdTableRow
	ThresholdOutcome    string
	ThresholdSort       string
	ThresholdOrder      string
	ThresholdTableEmpty bool
	ThresholdHeaders    []exploreThresholdHeader
	ThresholdTableOpen  bool
	ThresholdData       []exploreThresholdRecord
	ThresholdSelected   string
	ThresholdDefault    string
}
type exploreThresholdTableRow struct {
	history.Threshold
	Season  string
	Outcome string
	URL     string
	Hidden  bool
}
type exploreThresholdHeader struct {
	Key   string
	Label string
	URL   string
	Sort  string
}
type exploreThresholdRow struct {
	history.SeasonThresholds
	URL         string
	Selected    bool
	RecordNotes []history.ThresholdFinish
}
type exploreThresholdRecord struct {
	Season   string              `json:"season"`
	Playoffs int                 `json:"playoffs"`
	Home     int                 `json:"home"`
	Bye      int                 `json:"bye"`
	Round    string              `json:"round"`
	Values   []history.Threshold `json:"values"`
}

func exploreThresholds(query url.Values, summaries []history.SeasonScoring, archive []cache.HistoricalSeason) (exploreThresholdView, error) {
	page := exploreThresholdView{}
	outcomes := history.Thresholds(summaries, archive)
	if len(outcomes) > 0 {
		page.ThresholdDefault = outcomes[len(outcomes)-1].Season
	}
	page.ThresholdSelected = page.ThresholdDefault
	if values, present := query["threshold-season"]; present {
		if len(values) != 1 || !slices.ContainsFunc(outcomes, func(o history.SeasonThresholds) bool { return o.Season == values[0] }) {
			return page, fmt.Errorf("invalid threshold-season selection")
		}
		page.ThresholdSelected = values[0]
	}
	penalties := ""
	if query.Get("penalties") == "exclude" {
		penalties = "&penalties=exclude"
	}
	for _, season := range outcomes {
		var notes []history.ThresholdFinish
		for _, value := range season.Values {
			if value.PPG == nil {
				continue
			}
			for _, finish := range []history.ThresholdFinish{value.Cutoff, value.Next} {
				if (finish.OfficialPoints != finish.Earned || finish.OfficialPlayed != finish.Played) && !slices.ContainsFunc(notes, func(f history.ThresholdFinish) bool { return f.ID == finish.ID }) {
					notes = append(notes, finish)
				}
			}
		}
		page.ThresholdRows = append(page.ThresholdRows, exploreThresholdRow{
			SeasonThresholds: season, Selected: season.Season == page.ThresholdSelected, RecordNotes: notes,
			URL: "?view=thresholds&threshold-season=" + season.Season + penalties,
		})
		page.ThresholdData = append(page.ThresholdData, exploreThresholdRecord{
			Season: season.Season, Playoffs: season.Playoffs, Home: season.Home, Bye: season.Bye, Round: season.Round, Values: season.Values,
		})
	}
	outcomeKeys := []string{"shield", "home", "bye", "playoffs"}
	keys := []string{"season", "team", "points", "played", "ppg"}
	labels := []string{"Year", "Team", "Points", "Matches", "PPG"}
	page.ThresholdOutcome = "playoffs"
	sortKey, order := "season", "desc"
	for _, field := range []struct {
		key     string
		value   *string
		allowed []string
	}{
		{"threshold-outcome", &page.ThresholdOutcome, outcomeKeys},
		{"threshold-sort", &sortKey, keys},
		{"threshold-order", &order, []string{"asc", "desc"}},
	} {
		if values, present := query[field.key]; present {
			if len(values) != 1 || !slices.Contains(field.allowed, values[0]) {
				return page, fmt.Errorf("invalid %s selection", field.key)
			}
			*field.value = values[0]
		}
	}
	page.ThresholdSort, page.ThresholdOrder = sortKey, order
	page.ThresholdTableEmpty = true
	page.ThresholdTableOpen = query.Has("threshold-sort") || query.Has("threshold-outcome")
	for i, key := range keys {
		nextOrder, aria := "desc", "none"
		if key == "team" {
			nextOrder = "asc"
		}
		if key == sortKey {
			aria = "descending"
			if order == "desc" {
				nextOrder = "asc"
			} else {
				aria = "ascending"
				nextOrder = "desc"
			}
		}
		page.ThresholdHeaders = append(page.ThresholdHeaders, exploreThresholdHeader{
			Key: key, Label: labels[i], Sort: aria,
			URL: "?view=thresholds&threshold-season=" + page.ThresholdSelected + penalties + "&threshold-outcome=" + page.ThresholdOutcome + "&threshold-sort=" + key + "&threshold-order=" + nextOrder,
		})
	}
	for _, row := range page.ThresholdRows {
		for i, value := range row.Values {
			if value.PPG == nil {
				continue
			}
			if outcomeKeys[i] == page.ThresholdOutcome {
				page.ThresholdTableEmpty = false
			}
			page.ThresholdTableRows = append(page.ThresholdTableRows, exploreThresholdTableRow{
				Threshold: value, Season: row.Season, Outcome: outcomeKeys[i], Hidden: outcomeKeys[i] != page.ThresholdOutcome,
				URL: row.URL + "&threshold-outcome=" + page.ThresholdOutcome + "&threshold-sort=" + sortKey + "&threshold-order=" + order,
			})
		}
	}
	slices.SortStableFunc(page.ThresholdTableRows, func(a, b exploreThresholdTableRow) int {
		result := 0
		switch sortKey {
		case "team":
			result = strings.Compare(a.Cutoff.Name, b.Cutoff.Name)
		case "points":
			result = a.Cutoff.OfficialPoints - b.Cutoff.OfficialPoints
		case "played":
			result = a.Cutoff.OfficialPlayed - b.Cutoff.OfficialPlayed
		case "ppg":
			if *a.PPG < *b.PPG {
				result = -1
			} else if *a.PPG > *b.PPG {
				result = 1
			}
		default:
			result = strings.Compare(a.Season, b.Season)
		}
		if result == 0 {
			return strings.Compare(b.Season, a.Season)
		}
		if order == "desc" {
			result = -result
		}
		return result
	})
	return page, nil
}

func (exploreThresholdView) ThresholdPPG(value *float64) string {
	if value == nil {
		return "Unavailable"
	}
	return fmt.Sprintf("%.3f", *value)
}

// ThresholdFinish formats a team's finish as "Team · 4th · 40 pts · 1.54".
func (exploreThresholdView) ThresholdFinish(finish history.ThresholdFinish, ppg *float64) string {
	if ppg == nil {
		return "Unavailable"
	}
	return fmt.Sprintf("%s · %s · %d pts · %.2f", finish.Name, ordinalRank(finish.Place), finish.OfficialPoints, *ppg)
}

// ThresholdOutcomeControl picks the outcome the All seasons table lists.
func (page exploreThresholdView) ThresholdOutcomeControl() exploreSegmentGroup {
	return exploreSegmentGroup{Legend: "Outcome", Name: "threshold-outcome", Inline: true, Options: exploreSegments(page.ThresholdOutcome,
		exploreSegment{Value: "shield", Label: "Shield"}, exploreSegment{Value: "home", Label: "Home playoff game"},
		exploreSegment{Value: "bye", Label: "Playoff bye"}, exploreSegment{Value: "playoffs", Label: "Playoffs"})}
}
