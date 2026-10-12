package app

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/apptest"
	"github.com/jrduncans/nwsl-season/internal/history"
)

func TestExploreTeamSeasonsExtent(t *testing.T) {
	// These teamSeasonsExtent cases and testGeometryRankingBars in
	// e2e/geometry_test.go must change together: server and browser bounds agree.
	for _, tc := range []struct {
		name     string
		values   []float64
		signed   bool
		points   bool
		min, max float64
	}{
		{"empty", []float64{}, false, false, 0, .5},
		{"signed zero", []float64{0, 0}, true, false, -.5, .5},
		{"positive", []float64{1, 2}, false, false, 0, 2.5},
		{"signed", []float64{-2, 1}, true, false, -2.5, 2.5},
		{"round end", []float64{2.5, 2.37}, false, false, 0, 3},
		{"large", []float64{1e9}, false, false, 0, 2e9},
		{"points bounded", []float64{3, 2.9}, false, true, 0, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			extent := 0.0
			for _, value := range tc.values {
				extent = math.Max(extent, math.Abs(value))
			}
			gotMax := teamSeasonsExtent(extent, tc.points)
			gotMin := 0.0
			if tc.signed {
				gotMin = -gotMax
			}
			if gotMin != tc.min || gotMax != tc.max {
				t.Fatalf("bounds = [%g, %g], want [%g, %g]", gotMin, gotMax, tc.min, tc.max)
			}
		})
	}
}

func TestExploreTeamSeasonsHeadings(t *testing.T) {
	archive := apptest.Scenario(t, apptest.ScenarioTeamHistory)
	for _, tc := range []struct {
		measure, penalties string
		labels             [3]string
	}{
		{"points", "include", [3]string{"Points", "Expected points", "Points compared with expected,"}},
		{"for", "include", [3]string{"Goals scored", "Expected goals scored", "Goals scored compared with expected,"}},
		{"against", "include", [3]string{"Goals allowed", "Expected goals allowed", "Goals allowed compared with expected,"}},
		{"difference", "include", [3]string{"Goal differential", "Expected goal differential", "Goal differential compared with expected,"}},
		{"points", "exclude", [3]string{"Points", "Expected points", "Points compared with expected,"}},
		{"for", "exclude", [3]string{"Non-penalty goals scored", "Expected non-penalty goals scored", "Non-penalty goals scored compared with expected,"}},
		{"against", "exclude", [3]string{"Non-penalty goals allowed", "Expected non-penalty goals allowed", "Non-penalty goals allowed compared with expected,"}},
		{"difference", "exclude", [3]string{"Non-penalty goal differential", "Expected non-penalty goal differential", "Non-penalty goal differential compared with expected,"}},
	} {
		for i, by := range teamSeasonsRankBys {
			query := url.Values{"view": {"team-seasons"}, "team-seasons-measure": {tc.measure}, "team-seasons-rank-by": {by}, "penalties": {tc.penalties}}
			code, body := getPage(t, archive, "/explore?"+query.Encode())
			if code != http.StatusOK {
				t.Fatalf("%s: status %d", query.Encode(), code)
			}
			heading := tc.labels[i] + " per match, since 2016"
			requireExact(t, body, "[data-team-seasons-title]", heading)
			requireAttr(t, body, "[data-team-seasons-chart]", "aria-label", heading+", ranked "+by+" values with comparisons")
		}
	}
}

func TestExploreTeamSeasonsArchiveAndModes(t *testing.T) {
	archive := apptest.Scenario(t, apptest.ScenarioTeamHistory)
	store := &archiveHTTPStore{archive: archive}
	response := httptest.NewRecorder()
	NewHandler(store).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/explore?view=team-seasons", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status %d: %s", response.Code, response.Body.String())
	}
	if store.archiveCalls != 1 || store.seasonCalls != 0 || store.statusCalls != 0 {
		t.Fatalf("unexpected reads: %+v", store)
	}
	body := response.Body.String()
	requireExact(t, body, "title", "Team-seasons · Explore · NWSL Season Explorer")
	forbidRaw(t, body, "data-team-seasons-detail", "data-team-seasons-hint", `name="team-season"`, "data-team-seasons-link")
	if len(find(t, body, "[data-team-seasons-values][open]")) != 0 {
		t.Fatal("default table expanded")
	}
	requireExact(t, body, "[data-team-seasons-summary]", "Table: all 144 team-seasons")
	requireAttr(t, body, "[data-team-seasons-penalties]", "disabled", "")
	if len(find(t, body, "[data-team-seasons-controls] select")) != 3 || len(find(t, body, "[data-team-seasons-controls] select[name=team], [data-team-seasons-controls] select[name=season]")) != 0 {
		t.Fatal("unexpected controls")
	}
	if len(find(t, body, "[data-team-seasons-row]")) != 20 || len(find(t, body, "[data-team-seasons-rows] tr")) != 144 {
		t.Fatal("population or top/bottom count")
	}
	requireText(t, body, ".explore-archive-break", "124 more team-seasons")
	var payload exploreTeamSeasonsPayload
	if err := json.Unmarshal([]byte(text(find(t, body, "#explore-team-seasons-data")[0])), &payload); err != nil {
		t.Fatal(err)
	}
	for _, row := range payload.All {
		if row.LogoURL != clubLogoURL(row.TeamID) {
			t.Fatal("payload logo")
		}
	}
	if len(find(t, body, "[data-team-seasons-row] img")) != 20 || len(find(t, body, "[data-team-seasons-rows] img")) != 144 {
		t.Fatal("missing logos")
	}
	requireExact(t, body, "[data-team-seasons-axis] span", "3")
	for measureIndex, measure := range teamSeasonsMeasures {
		for byIndex, by := range teamSeasonsRankBys {
			path := "/explore?view=team-seasons&team-seasons-measure=" + measure + "&team-seasons-rank-by=" + by
			code, body := getPage(t, archive, path)
			if code != http.StatusOK {
				t.Fatalf("%s: %d", path, code)
			}
			ranked, missing := 0, 0
			for _, row := range payload.All {
				v := row.Values[measureIndex]
				if !v.Eligible {
					continue
				}
				if v.Ranks[byIndex] > 0 {
					ranked++
				} else {
					missing++
				}
			}
			if len(find(t, body, "[data-team-seasons-rows] tr")) != ranked+missing {
				t.Fatalf("%s population", path)
			}
			if by == "actual" {
				requireAttr(t, body, "[data-team-seasons-missing]", "hidden", "")
			} else {
				requireExact(t, body, "[data-team-seasons-missing]", fmt.Sprintf("%d team-seasons have %s and are not ranked. Their actual values appear last in the table.", missing, teamSeasonsMissingLabel(history.TeamSeasonMeasure(measureIndex), history.AllShots)))
			}
			if by == "gap" {
				requireText(t, body, "[data-team-seasons-legend]", "actual − expected")
				if len(find(t, body, ".explore-archive-marker")) != 0 {
					t.Fatal("gap companion")
				}
			} else {
				if len(find(t, body, ".explore-archive-marker")) == 0 {
					t.Fatal("missing comparison markers")
				}
			}
			if measure == "points" {
				requireAttr(t, body, "[data-team-seasons-penalties-control]", "hidden", "")
			} else {
				if attr(find(t, body, "[data-team-seasons-penalties]")[0], "disabled") != "" || len(find(t, body, "[data-team-seasons-penalties][disabled]")) != 0 {
					t.Fatal("goals basis disabled")
				}
			}
		}
	}
}

func TestExploreTeamSeasonsIndependentCoverageAndGate(t *testing.T) {
	archive := apptest.Scenario(t, apptest.ScenarioTeamHistory)
	// The reverse case is retained by the in-memory archive; SQLite's validated
	// observation store cannot retain xPoints when xG is unavailable.
	for _, tc := range []struct{ id, measure, missing string }{
		{"315VnJ759x", "points", "xPts incomplete"}, {"odMX2OJqYL", "for", "xG incomplete"},
	} {
		for _, by := range teamSeasonsRankBys {
			_, body := getPage(t, archive, "/explore?view=team-seasons&team-seasons-measure="+tc.measure+"&team-seasons-rank-by="+by)
			rows := find(t, body, fmt.Sprintf(`[data-team-seasons-rows] tr[data-team-id="%s"][data-season="2026"]`, tc.id))
			if len(rows) != 1 {
				t.Fatalf("%s %s: missing team-season", tc.measure, by)
			}
			rank := text(rows[0].FirstChild)
			if (rank == tc.missing) != (by != "actual") {
				t.Fatalf("%s %s: rank %s", tc.measure, by, rank)
			}
		}
	}
	_, body := getPage(t, apptest.TeamHistoryBelowGate(t), "/explore?view=team-seasons")
	if len(find(t, body, "[data-team-seasons-rows] tr")) != 128 || len(find(t, body, `[data-team-seasons-rows] tr[data-season="2026"]`)) != 0 {
		t.Fatal("below-gate population")
	}
	_, body = getPage(t, archive, "/explore?view=team-seasons")
	requireText(t, body, "[data-team-seasons-rows]", "in progress through 12 matches")
	_, body = getPage(t, testArchive(t, map[string]archiveState{}), "/explore?view=team-seasons")
	requireText(t, body, "[data-team-seasons-chart-empty]", "No ranked team-seasons")
}

func TestExploreTeamSeasonsTableExpansionAndNoBreak(t *testing.T) {
	archive := apptest.TeamHistorySmall(t)
	for _, suffix := range []string{"", "&team-seasons-sort=rank", "&team-seasons-order=asc"} {
		_, body := getPage(t, archive, "/explore?view=team-seasons"+suffix)
		requireExact(t, body, "[data-team-seasons-summary]", "Table: all 16 team-seasons")
		requireText(t, body, "[data-team-seasons-high]", "Highest points per match")
		requireText(t, body, "[data-team-seasons-low]", "Lowest points per match")
		requireAttr(t, body, "[data-team-seasons-break]", "hidden", "")
		if len(find(t, body, "[data-team-seasons-row]")) != 16 {
			t.Fatal("small population")
		}
		if (len(find(t, body, "[data-team-seasons-values][open]")) > 0) != (suffix != "") {
			t.Fatal("sort expansion")
		}
	}
	// Preserve the established handling of view; this feature owns no new rules.
	for _, query := range []string{"view=", "view=team-seasons&view=trend"} {
		code, _ := getPage(t, archive, "/explore?"+query)
		if code != 200 {
			t.Fatalf("view validation changed: %s %d", query, code)
		}
	}
}

func TestExploreTeamSeasonsRejectsInvalidState(t *testing.T) {
	archive := apptest.Scenario(t, apptest.ScenarioTeamHistory)
	var queries []string
	for key, allowed := range map[string][]string{"team-seasons-measure": teamSeasonsMeasures, "team-seasons-rank-by": teamSeasonsRankBys, "team-seasons-sort": teamSeasonsSortKeys, "team-seasons-order": {"asc", "desc"}, "penalties": {"include", "exclude"}} {
		queries = append(queries, key+"=", key+"=bad", key+"="+allowed[0]+"&"+key+"="+allowed[0])
	}
	for _, q := range queries {
		code, body := getPage(t, archive, "/explore?view=team-seasons&"+q)
		if code != 400 {
			t.Errorf("%s: %d", q, code)
		}
		requireText(t, body, "h1", "Invalid Explore selection")
	}
}

func TestExploreTeamSeasonsSortAndPointsBasis(t *testing.T) {
	value := func(v float64) *float64 { return &v }
	for _, order := range []string{"asc", "desc"} {
		rows := []exploreTeamSeasonRow{{exploreTeamSeasonRecord: exploreTeamSeasonRecord{TeamSeason: history.TeamSeason{TeamID: "a"}}, Rank: 2, Actual: value(2), Gap: value(.5001)}, {exploreTeamSeasonRecord: exploreTeamSeasonRecord{TeamSeason: history.TeamSeason{TeamID: "b"}}, Rank: 1, Actual: value(1), Gap: value(.5004)}, {exploreTeamSeasonRecord: exploreTeamSeasonRecord{TeamSeason: history.TeamSeason{TeamID: "c"}}, Actual: value(3)}}
		sortExploreTeamSeasons(rows, "gap", order)
		want := "b"
		if order == "asc" {
			want = "a"
		}
		if rows[0].TeamID != want || rows[2].TeamID != "c" {
			t.Fatal("unrounded sorting or unranked placement")
		}
		for _, key := range teamSeasonsSortKeys {
			sortExploreTeamSeasons(rows, key, order)
			if rows[2].TeamID != "c" {
				t.Fatal("unranked not last")
			}
		}
	}
	archive := penaltyArchive(t, 20)
	for _, by := range teamSeasonsRankBys {
		path := "/explore?view=team-seasons&team-seasons-rank-by=" + by
		_, all := getPage(t, archive, path)
		code, np := getPage(t, archive, path+"&penalties=exclude")
		if code != 200 {
			t.Fatal(code)
		}
		requireText(t, np, "[data-team-seasons-points-note]", pointsBasisNote)
		requireAttr(t, np, "[data-team-seasons-penalties-control]", "hidden", "")
		if text(find(t, all, "[data-team-seasons-rows]")[0]) != text(find(t, np, "[data-team-seasons-rows]")[0]) {
			t.Fatal("points values/population changed")
		}
		for _, link := range find(t, np, "[data-team-seasons-sort]") {
			u, err := url.Parse(attr(link, "href"))
			if err != nil {
				t.Fatal(err)
			}
			if u.Query().Has("penalties") {
				t.Fatal("stale penalties link")
			}
		}
	}
	// Incomplete non-penalty coverage cannot silently remove points rows.
	archive[0].Data.XGoals[0].Penalties = nil
	_, body := getPage(t, archive, "/explore?view=team-seasons&penalties=exclude")
	if len(find(t, body, "[data-team-seasons-rows] tr")) != 2 {
		t.Fatal("points basis eligibility")
	}
	_, body = getPage(t, archive, "/explore?view=team-seasons&team-seasons-measure=for&penalties=exclude")
	requireText(t, body, "[data-team-seasons-chart-empty]", "No ranked team-seasons")
	requireAttr(t, body, "[data-team-seasons-points-note]", "hidden", "")
	if len(find(t, body, "[data-team-seasons-rows] tr")) != 0 {
		t.Fatal("non-penalty integrity ignored")
	}
}
