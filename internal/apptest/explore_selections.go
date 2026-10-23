package apptest

// ExplorePointsOnlyCase describes one Explore URL's penalty visibility. The
// same cases exercise the Go selection predicate and browser behavior.
type ExplorePointsOnlyCase struct {
	Query      string
	Selector   string
	PointsOnly bool
}

// ExplorePointsOnlyCases is the shared URL matrix for Explore's Go and JS
// points-only selection predicates.
func ExplorePointsOnlyCases() []ExplorePointsOnlyCase {
	return []ExplorePointsOnlyCase{
		{Query: "", Selector: `[data-panel=teams] .penalties-select`},
		{Query: "metric=goals", Selector: `[data-panel=trend] .penalties-select`},
		{Query: "view=parity", Selector: `[data-panel=parity] .penalties-select`, PointsOnly: true},
		{Query: "view=teams&measure=points", Selector: `[data-team-controls] .penalties-select`, PointsOnly: true},
		{Query: "view=teams&measure=points&display=gap", Selector: `[data-team-controls] .penalties-select`, PointsOnly: true},
		{Query: "view=teams&measure=points&display=scatter", Selector: `[data-team-controls] .penalties-select`, PointsOnly: true},
		{Query: "view=teams&measure=points&display=table", Selector: `[data-team-controls] .penalties-select`},
		{Query: "view=teams&measure=points&display=quadrant", Selector: `[data-team-controls] .penalties-select`},
		{Query: "view=team-history&measure=points", Selector: `[data-panel=team-history] .penalties-select`, PointsOnly: true},
		{Query: "view=team-history&measure=for", Selector: `[data-panel=team-history] .penalties-select`},
		{Query: "view=season-trend&trend-view=points", Selector: `[data-trend-chart-card] .penalties-select`, PointsOnly: true},
		{Query: "view=season-trend&trend-view=relative&average-series=points", Selector: `[data-trend-chart-card] .penalties-select`, PointsOnly: true},
		{Query: "view=season-trend&trend-view=relative&average-series=xpoints", Selector: `[data-trend-chart-card] .penalties-select`, PointsOnly: true},
		{Query: "view=season-trend&trend-view=compare", Selector: `[data-trend-chart-card] .penalties-select`},
		{Query: "view=head-to-head&h2h-series=points", Selector: `[data-h2h-penalties-label]`, PointsOnly: true},
		{Query: "view=head-to-head&h2h-series=actual", Selector: `[data-h2h-penalties-label]`},
		{Query: "view=team-seasons", Selector: `[data-team-seasons-penalties-control]`, PointsOnly: true},
		{Query: "view=team-seasons&team-seasons-measure=for", Selector: `[data-team-seasons-penalties-control]`},
		{Query: "view=home-advantage", Selector: `[data-panel=home-advantage] .penalties-select`},
	}
}

// ExploreOpponentCase describes whether one Explore URL offers Adjust for
// opponents, for a season with opponent ratings.
type ExploreOpponentCase struct {
	Query string
	Shown bool
}

// ExploreOpponentCases is the shared URL matrix for Explore's Go and JS
// opponent-adjustment rule.
func ExploreOpponentCases() []ExploreOpponentCase {
	return []ExploreOpponentCase{
		{Query: "view=season-trend", Shown: true},
		{Query: "view=season-trend&trend-view=balance&smoothing=5", Shown: true},
		{Query: "view=season-trend&trend-view=difference&opponents=adjusted", Shown: true},
		{Query: "view=season-trend&trend-view=compare&opponents=adjusted"},
		{Query: "view=season-trend&trend-view=points", Shown: true},
		{Query: "view=season-trend&trend-view=points&smoothing=none&opponents=adjusted", Shown: true},
		{Query: "view=season-trend&trend-view=relative"},
		{Query: "view=team-history"},
		{Query: ""},
	}
}
