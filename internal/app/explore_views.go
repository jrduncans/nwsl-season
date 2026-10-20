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
// explore-views payload, for explore.js.
type exploreView struct {
	Key   string `json:"key"`
	Label string `json:"-"`
	// Displays are Compare teams' tabs; the first is the default display.
	Displays []exploreDisplay `json:"displays,omitempty"`
	// Defaults supply blank parameters for the view's rules.
	Defaults map[string]string `json:"defaults,omitempty"`
	// PointsOnly matches points-only selections, which ignore the scoring basis.
	PointsOnly exploreRule `json:"pointsOnly,omitempty"`
	// PointsNote matches selections that show points beside the non-penalty basis.
	PointsNote exploreRule `json:"pointsNote,omitempty"`
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
			Key: "teams",
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
		{Key: "team-rankings", Label: "Rankings"},
		{
			Key: "season-trend", Label: "Match by match",
			Defaults:      map[string]string{"trend-view": "balance", "average-series": "goals"},
			PointsOnly:    exploreRule{{"trend-view": {"points"}}, {"trend-view": {"relative"}, "average-series": {"points", "xpoints"}}},
			NeedsForecast: true,
		},
		{Key: "team-history", Label: "Season by season", PointsOnly: exploreRule{{"measure": {"points"}}}, PointsNote: exploreRule{{}}},
		{Key: "head-to-head", Label: "Head to head", PointsOnly: exploreRule{{"h2h-series": {"points"}}}, BothBases: true},
	}},
	{Key: "league", Label: "League trends", Views: []exploreView{
		{Key: "trend", Label: "Scoring trend"},
		{Key: "distribution", Label: "Goal distribution"},
		{Key: "home-advantage", Label: "Home advantage"},
		{Key: "parity", Label: "Parity", PointsOnly: exploreRule{{}}},
		{Key: "thresholds", Label: "Shield and playoff thresholds", BothBases: true, SkipsForecast: true},
		{
			Key: "team-seasons", Label: "Team-seasons",
			Defaults:   map[string]string{"team-seasons-measure": "points"},
			PointsOnly: exploreRule{{"team-seasons-measure": {"points"}}},
			BothBases:  true,
		},
		{Key: "table", Label: "Scoring table"},
	}},
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

func (view exploreView) displayLabel(key string) string {
	for _, display := range view.Displays {
		if display.Key == key {
			return display.Label
		}
	}
	return ""
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

// pointsOnlyExploreSelection identifies selections where the penalty basis
// cannot change any plotted value or eligibility rule.
func pointsOnlyExploreSelection(query url.Values) bool {
	view, ok := exploreViewFor(exploreRequestedView(query))
	return ok && view.PointsOnly.matches(query, view.Defaults)
}
