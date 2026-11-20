package app

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
)

// exploreRule is a declarative selection rule shared with explore.js: it
// matches when any alternative matches, and an alternative matches when every
// named parameter, or the view's default for a blank one, is an allowed value.
// An empty alternative always matches; a rule with no alternatives never does.
type exploreRule []map[string][]string

func (rule exploreRule) matches(query url.Values, defaults map[string]string) bool {
	return slices.ContainsFunc(rule, func(alternative map[string][]string) bool {
		for name, allowed := range alternative {
			value := query.Get(name)
			if value == "" {
				value = defaults[name]
			}
			if !slices.Contains(allowed, value) {
				return false
			}
		}
		return true
	})
}

// exploreViewGroup is one view group: its tab opens its first view.
type exploreViewGroup struct {
	Key   string        `json:"key"`
	Label string        `json:"-"`
	Views []exploreView `json:"views"`
}

// exploreView describes one Explore view for the server and, through the
// shared payload's views, for explore.js.
type exploreView struct {
	Key   string `json:"key"`
	Label string `json:"-"`
	// Slug names the view's path, /explore/<slug>; the default view has none
	// and lives at /explore. See exploreViewPath.
	Slug string `json:"slug,omitempty"`
	// Title and Description are the view page's <title> and meta
	// description. DocumentTitle is Title as the <title> shows it, which
	// explore.js sets when it switches views in place.
	Title         string `json:"-"`
	Description   string `json:"-"`
	DocumentTitle string `json:"title"`
	// Displays are Compare teams' tabs; the first is the default display.
	Displays []exploreDisplay `json:"displays,omitempty"`
	// Defaults supply blank parameters for the view's rules.
	Defaults map[string]string `json:"defaults,omitempty"`
	// PointsOnly matches points-only selections, which ignore the scoring basis.
	PointsOnly exploreRule `json:"pointsOnly,omitempty"`
	// PointsNote matches selections that show points beside the non-penalty basis.
	PointsNote exploreRule `json:"pointsNote,omitempty"`
	// OpponentAdjustment matches selections that offer Adjust for opponents.
	// Elsewhere a carried opponents value is ignored but kept.
	OpponentAdjustment exploreRule `json:"opponentAdjustment,omitempty"`
	// BothBases views carry both scoring bases, so switching basis in place
	// needs no reload.
	BothBases bool `json:"bothBases,omitempty"`
	// A SkipsForecast request renders no forecast benchmarks, so entering a
	// NeedsForecast view from it reloads the page.
	SkipsForecast bool `json:"skipsForecast,omitempty"`
	NeedsForecast bool `json:"needsForecast,omitempty"`
}

type exploreDisplay struct {
	Key   string `json:"key"`
	Label string `json:"-"`
}

// exploreViewGroups lists every Explore view in tab order. The first view of
// the first group is the default view.
var exploreViewGroups = []exploreViewGroup{
	{Key: "teams", Label: "Compare teams", Views: []exploreView{
		{
			Key:         "teams",
			Title:       "NWSL teams: actual vs expected",
			Description: "How each NWSL team's goals, points and goals allowed compare with xG and xPoints in a season, as a chart, gap, outlier plot or table.",
			Displays: []exploreDisplay{
				{"chart", "Actual vs expected"}, {"gap", "Gap to expected"}, {"scatter", "Outlier plot"},
				{"quadrant", "Scored vs allowed"}, {"table", "Table"},
			},
			Defaults:   map[string]string{"display": "chart", "measure": "difference"},
			PointsOnly: exploreRule{{"display": {"chart", "gap", "scatter"}, "measure": {"points"}}},
			PointsNote: exploreRule{{"display": {"table"}}},
		},
	}},
	{Key: "profile", Label: "Team profile", Views: []exploreView{
		{
			Key: "team-rankings", Label: "Rankings", Slug: "rankings",
			Title: "NWSL team rankings", Description: "Where an NWSL team ranked in the league each season, measure by measure.",
		},
		{
			Key: "season-trend", Label: "Match by match", Slug: "match-by-match",
			Title: "NWSL team form match by match", Description: "An NWSL team's results, goals and xG match by match through a season, with rolling averages.",
			Defaults:           map[string]string{"trend-view": "balance", "average-series": "goals"},
			PointsOnly:         exploreRule{{"trend-view": {"points"}}, {"trend-view": {"relative"}, "average-series": {"points", "xpoints"}}},
			OpponentAdjustment: exploreRule{{"trend-view": {"balance", "difference", "points"}}},
			NeedsForecast:      true,
		},
		{
			Key: "team-history", Label: "Season by season", Slug: "season-by-season",
			Title: "NWSL team history season by season", Description: "An NWSL team's goals, xG, points and xPoints season by season.",
			PointsOnly: exploreRule{{"measure": {"points"}}}, PointsNote: exploreRule{{}},
		},
		{
			Key: "head-to-head", Label: "Head to head", Slug: "head-to-head",
			Title: "NWSL head-to-head records", Description: "Every NWSL meeting between two teams, with results, goals and xG.",
			PointsOnly: exploreRule{{"h2h-series": {"points"}}}, BothBases: true,
		},
	}},
	{Key: "league", Label: "League trends", Views: []exploreView{
		{
			Key: "trend", Label: "Scoring trend", Slug: "scoring-trend",
			Title: "NWSL scoring trends by season", Description: "Goals and xG per match in every NWSL regular season.",
		},
		{
			Key: "distribution", Label: "Goal distribution", Slug: "goal-distribution",
			Title: "NWSL goal distribution by season", Description: "The share of NWSL regular-season matches with 0, 1, 2, 3 or 4+ goals, season by season.",
		},
		{
			Key: "home-advantage", Label: "Home advantage", Slug: "home-advantage",
			Title: "NWSL home advantage by season", Description: "How much better NWSL home teams do per match in goals, xG, points and xPoints, season by season.",
		},
		{
			Key: "parity", Label: "Parity", Slug: "parity",
			Title: "NWSL parity by season", Description: "How evenly matched NWSL teams were each season, measured by the spread of their points.",
			PointsOnly: exploreRule{{}},
		},
		{
			Key: "thresholds", Label: "Shield and playoff thresholds", Slug: "playoff-thresholds",
			Title: "NWSL points needed for the playoffs and Shield", Description: "How many points it took to win the NWSL Shield, earn a home playoff game or a bye, and make the playoffs each season.",
			PointsOnly: exploreRule{{}}, BothBases: true, SkipsForecast: true,
		},
		{
			Key: "team-seasons", Label: "Team-seasons", Slug: "team-seasons",
			Title: "Best and worst NWSL team-seasons", Description: "Every NWSL team-season ranked by points, goals, xG and more.",
			Defaults:   map[string]string{"team-seasons-measure": "points"},
			PointsOnly: exploreRule{{"team-seasons-measure": {"points"}}},
			BothBases:  true,
		},
		{
			Key: "table", Label: "Scoring table", Slug: "scoring-table",
			Title: "NWSL scoring by season", Description: "Goals, xG and goals minus xG per match for every NWSL regular season.",
		},
	}},
}

func init() {
	for i := range exploreViewGroups {
		for j := range exploreViewGroups[i].Views {
			view := &exploreViewGroups[i].Views[j]
			view.DocumentTitle = documentTitle(view.Title)
		}
	}
}

// exploreViewPath is the view's page, from the site root: /explore/<slug>,
// or /explore for the default view. Selections stay in the query string.
func exploreViewPath(view exploreView) string {
	if view.Slug == "" {
		return "/explore"
	}
	return "/explore/" + view.Slug
}

// exploreViewForSlug finds the view a /explore/<slug> path names.
func exploreViewForSlug(slug string) (exploreView, bool) {
	for _, group := range exploreViewGroups {
		for _, view := range group.Views {
			if slug != "" && view.Slug == slug {
				return view, true
			}
		}
	}
	return exploreView{}, false
}

func exploreViewFor(key string) (exploreView, bool) {
	for _, group := range exploreViewGroups {
		for _, view := range group.Views {
			if view.Key == key {
				return view, true
			}
		}
	}
	return exploreView{}, false
}

func exploreViewKeys() []string {
	var keys []string
	for _, group := range exploreViewGroups {
		for _, view := range group.Views {
			keys = append(keys, view.Key)
		}
	}
	return keys
}

func exploreGroupOf(key string) string {
	for _, group := range exploreViewGroups {
		for _, view := range group.Views {
			if view.Key == key {
				return group.Key
			}
		}
	}
	return ""
}

func (view exploreView) defaultDisplay() string {
	if len(view.Displays) == 0 {
		return ""
	}
	return view.Displays[0].Key
}

func (view exploreView) displayKeys() []string {
	var keys []string
	for _, display := range view.Displays {
		keys = append(keys, display.Key)
	}
	return keys
}

// exploreRequestedView returns the view a query asks for, before validation.
// Scoring trend was once the default view, so its metric links omitted it.
func exploreRequestedView(query url.Values) string {
	if view := query.Get("view"); view != "" {
		return view
	}
	if query.Has("metric") {
		return "trend"
	}
	return exploreViewGroups[0].Views[0].Key
}

func unknownExploreViewError() error {
	keys := exploreViewKeys()
	return fmt.Errorf("view must be %s, or %s", strings.Join(keys[:len(keys)-1], ", "), keys[len(keys)-1])
}

// opponentAdjustmentSelection reports whether a selection offers Adjust for
// opponents, given a season with opponent ratings.
func opponentAdjustmentSelection(query url.Values) bool {
	view, ok := exploreViewFor(exploreRequestedView(query))
	return ok && view.OpponentAdjustment.matches(query, view.Defaults)
}

// pointsOnlyExploreSelection identifies selections where the penalty basis
// cannot change any plotted value or eligibility rule.
func pointsOnlyExploreSelection(query url.Values) bool {
	view, ok := exploreViewFor(exploreRequestedView(query))
	return ok && view.PointsOnly.matches(query, view.Defaults)
}
