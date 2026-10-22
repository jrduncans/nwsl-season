package app

import (
	"fmt"
	"math"
	"net/http"
	"net/url"
	"slices"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/apptest"
	"github.com/jrduncans/nwsl-season/internal/history"
)

const opponentsTeam = "odMX2OJqYL"

func TestExploreOpponentsSelectionIsValidated(t *testing.T) {
	archive := apptest.Scenario(t, apptest.ScenarioSeasonTrend)
	for _, query := range []string{"opponents=", "opponents=adjusted&opponents=adjusted", "opponents=on", "trend-view=compare&opponents=Adjusted"} {
		code, body := getPage(t, archive, "/explore?view=season-trend&"+query)
		if code != http.StatusBadRequest {
			t.Errorf("%s status = %d, want 400", query, code)
		}
		requireText(t, body, "main", "opponents must be adjusted")
	}
	if code, _ := getPage(t, archive, "/explore?view=season-trend&opponents=adjusted"); code != http.StatusOK {
		t.Fatalf("adjusted status = %d", code)
	}
}

func TestOpponentAdjustmentPredicateMatchesSharedCases(t *testing.T) {
	for _, selection := range apptest.ExploreOpponentCases() {
		query, err := url.ParseQuery(selection.Query)
		if err != nil {
			t.Fatal(err)
		}
		if got := opponentAdjustmentSelection(query); got != selection.Shown {
			t.Errorf("opponentAdjustmentSelection(%q) = %v, want %v", selection.Query, got, selection.Shown)
		}
	}
}

// Both bases share each match's factors, computed from full xG; excluding
// penalties changes only the values divided.
func TestExploreOpponentAdjustmentDividesTheSelectedBasis(t *testing.T) {
	archive := apptest.Scenario(t, apptest.ScenarioSeasonTrend)
	factors := map[string]exploreOpponentFactors{}
	for _, basis := range []history.ScoringBasis{history.AllShots, history.NonPenalty} {
		summaries, err := history.SummarizeScoringBasis(archive, basis)
		if err != nil {
			t.Fatal(err)
		}
		query := url.Values{"season": {"2025"}, "team": {opponentsTeam}, "smoothing": {"none"}, "opponents": {"adjusted"}}
		if basis.ExcludesPenalties() {
			query.Set("penalties", "exclude")
		}
		teams, err := exploreTeams(query, summaries, archive)
		if err != nil {
			t.Fatal(err)
		}
		trend, err := exploreSeasonTrend(query, teams, teamNameView{ID: opponentsTeam})
		if err != nil {
			t.Fatal(err)
		}
		if !trend.TrendAdjusted || len(trend.TrendRows) == 0 {
			t.Fatalf("basis %v: adjustment not applied", basis)
		}
		goalsFor := "Adjusted goals for"
		if basis.ExcludesPenalties() {
			goalsFor = "Adjusted non-penalty goals for"
		}
		column := slices.IndexFunc(trend.TrendTableColumns, func(column exploreTrendColumn) bool { return column.Label == goalsFor })
		xgAgainst := slices.IndexFunc(trend.TrendTableColumns, func(column exploreTrendColumn) bool { return column.Label == basisText(basis, "Adjusted xG against") })
		if column < 0 || xgAgainst < 0 {
			t.Fatalf("basis %v columns lack %q or adjusted xG against", basis, goalsFor)
		}
		for _, row := range trend.TrendRows {
			if previous, ok := factors[row.ID]; ok && previous != *row.Opponents {
				t.Fatalf("match %s factors differ by basis: %+v, %+v", row.ID, previous, *row.Opponents)
			}
			factors[row.ID] = *row.Opponents
			if want := fmt.Sprintf("%.2f", row.Values[exploreScored].Actual/row.Opponents.Scored); row.DisplayValues[column] != want {
				t.Errorf("basis %v match %s %s = %s, want %s", basis, row.ID, goalsFor, row.DisplayValues[column], want)
			}
			if want := exploreTrendNumber(new(*row.Values[exploreAllowed].Expected / row.Opponents.Allowed)); row.DisplayValues[xgAgainst] != want {
				t.Errorf("basis %v match %s adjusted xG against = %s, want %s", basis, row.ID, row.DisplayValues[xgAgainst], want)
			}
		}
	}
}

func TestExploreOpponentAdjustmentPage(t *testing.T) {
	archive := apptest.Scenario(t, apptest.ScenarioSeasonTrend)
	path := "/explore?view=season-trend&team=" + opponentsTeam + "&smoothing=none&opponents=adjusted"
	code, body := getPage(t, archive, path+"&season=2025")
	if code != http.StatusOK {
		t.Fatal(code)
	}
	forbidElements(t, body, "[data-trend-opponents-control][hidden]")
	requireElements(t, body, "[data-trend-opponents][checked]")
	requireExact(t, body, "[data-season-trend-note]", opponentAdjustmentNote)
	requireText(t, body, "[data-trend-margin-tiles]", "adjusted goals", "adjusted xG")
	requireExact(t, body, "[data-trend-margin-official]", "W–D–L and points use official results.")
	forbidElements(t, body, "[data-trend-margin-official][hidden]")
	requireText(t, body, "[data-trend-headings]", "Adjusted goals for", "Adjusted goals against", "Adjusted xG for", "Adjusted xG against")
	requireAttr(t, body, "[data-trend-sort=adjusted-xg-for]", "href", "?opponents=adjusted&season=2025&smoothing=none&team="+opponentsTeam+"&trend-order=desc&trend-sort=adjusted-xg-for&view=season-trend")

	// The active season's ratings lack one match's xG, which the note says.
	_, active := getPage(t, archive, path+"&season=2026")
	requireExact(t, active, "[data-season-trend-note]", opponentAdjustmentNote+" Opponent ratings use 231 of 232 matches with xG.")

	// Rolling lines average adjusted values under adjusted labels.
	_, rolling := getPage(t, archive, "/explore?view=season-trend&team="+opponentsTeam+"&opponents=adjusted&season=2025&trend-view=difference")
	requireText(t, rolling, "[data-trend-headings]", "Adjusted goal margin", "Adjusted xG margin")

	// Sorting by an adjusted column needs the adjustment.
	if code, _ := getPage(t, archive, path+"&season=2025&trend-sort=adjusted-xg-for"); code != http.StatusOK {
		t.Fatalf("adjusted sort status = %d", code)
	}
	if code, _ := getPage(t, archive, "/explore?view=season-trend&season=2025&trend-sort=adjusted-xg-for"); code != http.StatusFound {
		t.Fatalf("unadjusted sort by adjusted column status = %d, want 302", code)
	}
}

// Other charts, and seasons without xG, hide the checkbox, ignore a carried
// value and keep it.
func TestExploreOpponentAdjustmentIsIgnoredWhereHidden(t *testing.T) {
	archive := apptest.Scenario(t, apptest.ScenarioSeasonTrend)
	_, compare := getPage(t, archive, "/explore?view=season-trend&season=2025&team="+opponentsTeam+"&trend-view=compare&opponents=adjusted")
	requireElements(t, compare, "[data-trend-opponents-control][hidden]", "[data-trend-opponents][checked]")
	forbidText(t, compare, "[data-trend-headings]", "Adjusted")

	for index := range archive {
		if archive[index].Entry.Season == "2025" {
			archive[index].Data.XGoals = nil
		}
	}
	code, body := getPage(t, archive, "/explore?view=season-trend&season=2025&team="+opponentsTeam+"&smoothing=none&opponents=adjusted")
	if code != http.StatusOK {
		t.Fatal(code)
	}
	requireElements(t, body, "[data-trend-opponents-control][hidden]", "[data-trend-opponents][checked]")
	forbidText(t, body, "[data-trend-headings]", "Adjusted")
	forbidText(t, body, "[data-trend-margin-tiles]", "adjusted")
}

// Normalized factors make the average opponent the season's own, so the
// league's adjusted totals match its official totals on average.
func TestExploreOpponentFactorsAverageOneEachSeason(t *testing.T) {
	archive := apptest.Scenario(t, apptest.ScenarioSeasonTrend)
	summaries, err := history.SummarizeScoringBasis(archive, history.AllShots)
	if err != nil {
		t.Fatal(err)
	}
	teams, err := exploreTeams(url.Values{}, summaries, archive)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, season := range teams.TeamSeasons {
		if season.Opponents == nil {
			continue
		}
		checked++
		var scored, allowed float64
		var count int
		for _, team := range season.Teams {
			for _, match := range team.Matches {
				scored += match.Opponents.Scored
				allowed += match.Opponents.Allowed
				count++
			}
		}
		if count == 0 || math.Abs(scored/float64(count)-1) > 1e-9 || math.Abs(allowed/float64(count)-1) > 1e-9 {
			t.Errorf("%s mean factors = %.6f scored, %.6f allowed over %d matches; want 1", season.Season, scored/float64(count), allowed/float64(count), count)
		}
	}
	if checked != 2 {
		t.Fatalf("checked %d seasons, want 2025 and 2026", checked)
	}
}
