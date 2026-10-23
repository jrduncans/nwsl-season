package app

import (
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strings"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/history"
)

// exploreParityView is the Parity tab: the spread of team points per game in
// each eligible season against the spread chance alone would give, and one
// selected season's teams on a PPG strip.
type exploreParityView struct {
	ParityData    []exploreParityRecord
	ParityRows    []exploreParityRow
	ParityColumns []exploreTeamColumn
	// ParitySelected is the strip's season; nil when no season is eligible.
	ParitySelected *exploreParityRecord
	// ParityDefaultSeason is the newest completed season, or the newest
	// season when none is completed; ParitySeason is the strip's season.
	ParityDefaultSeason, ParitySeason string
	ParitySeries                      string
	ParitySort, ParityOrder           string
	ParityExpanded                    bool
	ParityActiveStatus                string
	ParityXPointsMissing              string
	ParitySeasonNote                  string
}

// exploreParityRecord is one season's chart, table and strip values. The
// quartiles come from history.Quantile so the chart never computes its own.
type exploreParityRecord struct {
	Season  string              `json:"season"`
	Active  bool                `json:"active"`
	Played  int                 `json:"played"`
	Matches float64             `json:"matches"`
	Spread  float64             `json:"spread"`
	Chance  float64             `json:"chance"`
	XSpread *float64            `json:"xspread"`
	Gap     float64             `json:"gap"`
	Q1      float64             `json:"q1"`
	Q3      float64             `json:"q3"`
	IQR     float64             `json:"iqr"`
	Teams   []exploreParityTeam `json:"teams"`
	// Top and Bottom name every team tied at the best and worst PPG.
	Top    []string `json:"top"`
	Bottom []string `json:"bottom"`
}

type exploreParityTeam struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Logo   string   `json:"logo"`
	Played int      `json:"played"`
	PPG    float64  `json:"ppg"`
	XPPG   *float64 `json:"xppg"`
}

type exploreParityRow struct {
	exploreParityRecord
	SeasonURL string
	Selected  bool
}

var exploreParityKeys = []string{"season", "teams", "matches", "spread", "chance", "xspread", "gap", "iqr"}

func exploreParity(query url.Values, seasons []history.SeasonParity, teamSeasons []exploreTeamSeason) (exploreParityView, error) {
	page := exploreParityView{ParitySort: "season", ParityOrder: "desc", ParitySeries: "actual"}
	for _, field := range []struct {
		key     string
		value   *string
		allowed []string
		table   bool
	}{
		{"parity-sort", &page.ParitySort, exploreParityKeys, true},
		{"parity-order", &page.ParityOrder, []string{"asc", "desc"}, true},
		{"parity-series", &page.ParitySeries, []string{"actual", "both"}, false},
	} {
		if values, present := query[field.key]; present {
			if len(values) != 1 || !slices.Contains(field.allowed, values[0]) {
				return page, fmt.Errorf("invalid %s selection", field.key)
			}
			*field.value = values[0]
			page.ParityExpanded = page.ParityExpanded || field.table
		}
	}

	names := make(map[string]map[string]exploreTeamRecord, len(teamSeasons))
	for _, season := range teamSeasons {
		names[season.Season] = make(map[string]exploreTeamRecord, len(season.Teams))
		for _, team := range season.Teams {
			names[season.Season][team.ID] = team
		}
	}
	name := func(season, id string) string {
		if team, ok := names[season][id]; ok && team.Name != "" {
			return team.Name
		}
		return id
	}

	var active, missing []string
	defaultSeason := ""
	for _, season := range seasons {
		if !season.PlotEligible {
			continue
		}
		record := exploreParityRecord{
			Season: season.Season, Active: season.Lifecycle == cache.SourceScopeActive, Played: season.Played,
			Matches: season.MatchesPerTeam, Spread: season.Spread, Chance: season.Chance, XSpread: season.XSpread,
			Gap: season.Gap, Q1: season.Q1, Q3: season.Q3, IQR: season.IQR(),
		}
		for _, team := range season.Teams {
			record.Teams = append(record.Teams, exploreParityTeam{
				ID: team.TeamID, Name: name(season.Season, team.TeamID), Logo: names[season.Season][team.TeamID].LogoURL,
				Played: team.Played, PPG: team.PPG, XPPG: team.XPPG,
			})
		}
		for _, id := range season.Top {
			record.Top = append(record.Top, name(season.Season, id))
		}
		for _, id := range season.Bottom {
			record.Bottom = append(record.Bottom, name(season.Season, id))
		}
		page.ParityData = append(page.ParityData, record)
		if record.Active {
			active = append(active, fmt.Sprintf("%s: in progress through %d matches (%.1f per team)", season.Season, season.Played, season.MatchesPerTeam))
		} else {
			// Seasons arrive oldest first, so the last completed one wins.
			defaultSeason = season.Season
		}
		if season.XSpread == nil {
			missing = append(missing, season.Season)
		}
	}
	page.ParityActiveStatus = strings.Join(active, "; ")
	page.ParityXPointsMissing = strings.Join(missing, ", ")
	if len(page.ParityData) == 0 {
		return page, nil
	}
	if defaultSeason == "" {
		defaultSeason = page.ParityData[len(page.ParityData)-1].Season
	}

	// The season is shared with Compare teams, whose rule already rejects a
	// year outside the catalog. A catalog year without parity values falls
	// back to the default, so a selection carried from another view still opens.
	page.ParityDefaultSeason, page.ParitySeason = defaultSeason, defaultSeason
	if requested := query.Get("season"); requested != "" {
		if slices.ContainsFunc(page.ParityData, func(record exploreParityRecord) bool { return record.Season == requested }) {
			page.ParitySeason = requested
		} else {
			page.ParitySeasonNote = fmt.Sprintf("%s is not plotted, so the strip shows %s.", requested, defaultSeason)
		}
	}

	selection := url.Values{}
	for key, values := range query {
		selection[key] = values
	}
	selection.Set("view", "parity")
	for index := range page.ParityData {
		record := page.ParityData[index]
		seasonURL := exploreTeamURL(selection, map[string]string{"season": record.Season})
		page.ParityRows = append(page.ParityRows, exploreParityRow{exploreParityRecord: record, SeasonURL: seasonURL, Selected: record.Season == page.ParitySeason})
		if record.Season == page.ParitySeason {
			page.ParitySelected = &page.ParityData[index]
		}
	}

	sortExploreParity(page.ParityRows, page.ParitySort, page.ParityOrder)
	columns := []exploreTeamColumn{
		{Key: "season", Label: "Season", Description: "season"},
		{Key: "teams", Label: "Teams", Description: "number of teams"},
		{Key: "matches", Label: "Matches per team", Description: "average matches per team"},
		{Key: "spread", Label: "PPG spread", Description: "spread of points per game"},
		{Key: "chance", Label: "Chance level", Description: "spread expected from chance alone"},
		{Key: "xspread", Label: "xPPG spread", Description: "xPPG spread"},
		{Key: "gap", Label: "Top-to-bottom gap", Description: "best minus worst points per game"},
		{Key: "iqr", Label: "Middle half (IQR)", Description: "interquartile range of points per game"},
	}
	for _, column := range columns {
		column.Sort = "none"
		order := "desc"
		if column.Key == page.ParitySort {
			column.Sort, order = "descending", "asc"
			if page.ParityOrder == "asc" {
				column.Sort, order = "ascending", "desc"
			}
		}
		column.URL = exploreTeamURL(selection, map[string]string{"parity-sort": column.Key, "parity-order": order})
		page.ParityColumns = append(page.ParityColumns, column)
	}
	return page, nil
}

// ParityValue formats a points-per-game value for the Parity tab.
func (exploreParityView) ParityValue(value any) string {
	switch value := value.(type) {
	case float64:
		return fmt.Sprintf("%.2f", value)
	case *float64:
		if value != nil {
			return fmt.Sprintf("%.2f", *value)
		}
	}
	return "Unavailable"
}

// Summary names what drove the season's spread: the top-to-bottom gap with
// every team tied at each end, and the middle half of teams. explore.js
// writes the same sentence when the selection changes.
func (r exploreParityRecord) Summary() string {
	if len(r.Teams) == 0 {
		return ""
	}
	return fmt.Sprintf("Top-to-bottom gap: %.2f PPG, from %s (%.2f) down to %s (%.2f). Middle half of teams: %.2f to %.2f PPG (interquartile range %.2f).",
		r.Gap, strings.Join(r.Top, ", "), r.Teams[0].PPG, strings.Join(r.Bottom, ", "), r.Teams[len(r.Teams)-1].PPG, r.Q1, r.Q3, r.IQR)
}

// sortExploreParity orders rows from unrounded values. Unavailable values stay
// last in both directions, and ties show the newest season first.
func sortExploreParity(rows []exploreParityRow, column, order string) {
	value := func(row exploreParityRow) *float64 {
		number := func(value float64) *float64 { return &value }
		switch column {
		case "teams":
			return number(float64(len(row.Teams)))
		case "matches":
			return number(row.Matches)
		case "spread":
			return number(row.Spread)
		case "chance":
			return number(row.Chance)
		case "xspread":
			return row.XSpread
		case "gap":
			return number(row.Gap)
		case "iqr":
			return number(row.IQR)
		}
		return nil
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if column != "season" {
			x, y := value(a), value(b)
			if (x == nil) != (y == nil) {
				return x != nil
			}
			if x != nil && *x != *y {
				if order == "asc" {
					return *x < *y
				}
				return *x > *y
			}
		} else if a.Season != b.Season {
			if order == "asc" {
				return a.Season < b.Season
			}
			return a.Season > b.Season
		}
		return a.Season > b.Season
	})
}

// ParitySeriesControl adds the xPPG spread beside the PPG spread.
func (page exploreParityView) ParitySeriesControl() exploreSegmentGroup {
	return exploreSegmentGroup{Legend: "Show", Name: "parity-series", Form: "parity-controls", Options: exploreSegments(page.ParitySeries,
		exploreSegment{Value: "actual", Label: "Points"}, exploreSegment{Value: "both", Label: "Points and xPoints"})}
}
