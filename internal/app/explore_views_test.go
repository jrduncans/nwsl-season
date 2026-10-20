package app

import (
	"encoding/json"
	"net/url"
	"slices"
	"testing"
)

func TestExploreRuleMatchesAlternativesWithDefaults(t *testing.T) {
	rule := exploreRule{{"display": {"chart", "gap"}, "measure": {"points"}}, {"display": {"table"}}}
	defaults := map[string]string{"display": "chart"}
	for query, want := range map[string]bool{
		"measure=points":              true,
		"display=&measure=points":     true,
		"display=gap&measure=points":  true,
		"display=gap&measure=for":     false,
		"display=table":               true,
		"display=quadrant":            false,
		"":                            false,
		"display=scatter&measure=for": false,
	} {
		values, err := url.ParseQuery(query)
		if err != nil {
			t.Fatal(err)
		}
		if got := rule.matches(values, defaults); got != want {
			t.Errorf("matches(%q) = %v, want %v", query, got, want)
		}
	}
	if (exploreRule{{}}).matches(url.Values{}, nil) != true {
		t.Error("an empty alternative always matches")
	}
	if (exploreRule(nil)).matches(url.Values{}, nil) {
		t.Error("a rule without alternatives never matches")
	}
}

func TestExploreViewKeysAreUniqueAndGroupsLeadWithTheirFirstView(t *testing.T) {
	var keys []string
	for _, group := range exploreViewGroups {
		if len(group.Views) == 0 {
			t.Fatalf("group %s has no views", group.Key)
		}
		for _, view := range group.Views {
			if slices.Contains(keys, view.Key) {
				t.Errorf("duplicate view key %s", view.Key)
			}
			keys = append(keys, view.Key)
			if view.Label == "" && len(view.Displays) == 0 {
				t.Errorf("view %s has no label", view.Key)
			}
		}
	}
	if got := exploreViewKeys(); !slices.Equal(got, keys) {
		t.Errorf("exploreViewKeys() = %v, want %v", got, keys)
	}
	if view, ok := exploreViewFor("teams"); !ok || view.defaultDisplay() != "chart" {
		t.Errorf("Compare teams default display = %q", view.defaultDisplay())
	}
}

// The rendered tabs, panels and emitted registry all follow the one registry,
// so a view cannot be added to one and forgotten in the others.
func TestExploreRegistryMatchesRenderedTabsPanelsAndPayload(t *testing.T) {
	_, body := getPage(t, penaltyArchive(t, 20), "/explore")
	var panels, tabs, groups []string
	for _, node := range find(t, body, "[data-panel]") {
		panels = append(panels, attr(node, "data-panel"))
	}
	for _, node := range find(t, body, "[data-group-choice]") {
		groups = append(groups, attr(node, "data-group-choice"))
	}
	for _, node := range find(t, body, "nav [data-view-choice], nav [data-team-display]") {
		tabs = append(tabs, attr(node, "data-view-choice")+attr(node, "data-team-display"))
	}
	var wantGroups, wantTabs []string
	for _, group := range exploreViewGroups {
		wantGroups = append(wantGroups, group.Key)
		for _, view := range group.Views {
			if len(view.Displays) == 0 {
				wantTabs = append(wantTabs, view.Key)
			}
			for _, display := range view.Displays {
				wantTabs = append(wantTabs, display.Key)
			}
		}
	}
	slices.Sort(panels)
	wantPanels := exploreViewKeys()
	slices.Sort(wantPanels)
	if !slices.Equal(panels, wantPanels) {
		t.Errorf("panels = %v, want %v", panels, wantPanels)
	}
	if !slices.Equal(groups, wantGroups) {
		t.Errorf("group tabs = %v, want %v", groups, wantGroups)
	}
	slices.Sort(tabs)
	slices.Sort(wantTabs)
	if !slices.Equal(tabs, wantTabs) {
		t.Errorf("view tabs = %v, want %v", tabs, wantTabs)
	}
	nodes := find(t, body, "script#explore-views")
	if len(nodes) != 1 || nodes[0].FirstChild == nil {
		t.Fatal("registry payload missing")
	}
	var emitted []exploreViewGroup
	if err := json.Unmarshal([]byte(nodes[0].FirstChild.Data), &emitted); err != nil {
		t.Fatalf("registry payload: %v", err)
	}
	if len(emitted) != len(exploreViewGroups) || len(emitted[1].Views) != len(exploreViewGroups[1].Views) || emitted[1].Views[1].Key != "season-trend" || !emitted[1].Views[1].NeedsForecast {
		t.Errorf("registry payload = %+v", emitted)
	}
}
