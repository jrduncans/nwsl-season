package app

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

// npxgSeasonData has two completed alpha-bravo games with xG. Alpha has 3.00
// xG for across them, 0.76 of it from a first-game penalty.
func npxgSeasonData(firstSplit, secondSplit *cache.PenaltySplit) cache.SeasonData {
	data := testSeasonData()
	data.Games = append(data.Games, cache.Game{
		ASAID: "completed-2", Season: "2026", Stage: "Regular Season", KickoffUTC: "2026-07-05 19:00:00 UTC",
		Status: standings.CompletedStatus, HomeTeamID: "bravo", AwayTeamID: "alpha",
		HomeScore: sql.NullInt64{Int64: 0, Valid: true}, AwayScore: sql.NullInt64{Int64: 1, Valid: true}, Matchday: sql.NullInt64{Int64: 2, Valid: true},
	})
	xg := func(id, home, away string, homeXG, awayXG float64, split *cache.PenaltySplit) cache.GameXG {
		return cache.GameXG{
			GameID: id, Availability: cache.XGAvailable, HomeTeamID: home, AwayTeamID: away,
			HomeXG: sql.NullFloat64{Float64: homeXG, Valid: true}, AwayXG: sql.NullFloat64{Float64: awayXG, Valid: true},
			Penalties: split,
		}
	}
	// Alpha: 2.36 + 0.64 = 3.00 xG for, 1.11 + 1.00 = 2.11 against.
	data.XGoals = []cache.GameXG{
		xg("completed", "alpha", "bravo", 2.36, 1.11, firstSplit),
		xg("completed-2", "bravo", "alpha", 1.00, 0.64, secondSplit),
	}
	return data
}

func rowFor(rows []tableRowView, id string) tableRowView {
	for _, row := range rows {
		if row.TeamID == id {
			return row
		}
	}
	return tableRowView{}
}

func TestAddXGValuesComputesNonPenaltyXG(t *testing.T) {
	alphaPenalty := &cache.PenaltySplit{HomeXG: .76, HomeGoals: 1, HomeShots: 1}
	bravoPenalty := &cache.PenaltySplit{AwayXG: 0, HomeXG: .2, HomeShots: 1}
	tests := []struct {
		name                 string
		first, second        *cache.PenaltySplit
		wantNPAvailable      int
		alphaNP, alphaNPPer  string
		alphaDiff, alphaPerD string
		alphaSortTotal       string
		bravoNP              string
	}{
		{
			name: "every match has a split", first: alphaPenalty, second: &cache.PenaltySplit{}, wantNPAvailable: 2,
			alphaNP: "2.24/2.11", alphaNPPer: "1.12/1.06", alphaDiff: "+0.13", alphaPerD: "+0.06", alphaSortTotal: "0.1300",
			bravoNP: "2.11/2.24",
		},
		{
			name: "penalties on both sides", first: alphaPenalty, second: bravoPenalty, wantNPAvailable: 2,
			alphaNP: "2.24/1.91", alphaNPPer: "1.12/0.96", alphaDiff: "+0.33", alphaPerD: "+0.16", alphaSortTotal: "0.3300",
			bravoNP: "1.91/2.24",
		},
		{
			name: "a team with an unsplit match shows dashes", first: alphaPenalty, second: nil, wantNPAvailable: 1,
			alphaNP: "—", alphaNPPer: "—", alphaDiff: "—", alphaPerD: "—", alphaSortTotal: "", bravoNP: "—",
		},
		{
			name: "no splits", wantNPAvailable: 0,
			alphaNP: "—", alphaNPPer: "—", alphaDiff: "—", alphaPerD: "—", alphaSortTotal: "", bravoNP: "—",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := npxgSeasonData(test.first, test.second)
			rows := []tableRowView{{TeamID: "alpha"}, {TeamID: "bravo"}}
			rows, coverage := addXGValues(rows, data)

			if coverage.Available != 2 || coverage.Completed != 2 || coverage.NPAvailable != test.wantNPAvailable {
				t.Fatalf("coverage = %+v, want 2 available / 2 completed / %d with splits", coverage, test.wantNPAvailable)
			}
			alpha, bravo := rowFor(rows, "alpha"), rowFor(rows, "bravo")
			if alpha.XGForAgainst != "3.00/2.11" {
				t.Fatalf("alpha xG = %q, want 3.00/2.11 (unchanged by penalties)", alpha.XGForAgainst)
			}
			if alpha.NPXGForAgainst != test.alphaNP || alpha.NPXGForAgainstPerGame != test.alphaNPPer || alpha.NPXGDifference != test.alphaDiff || alpha.NPXGDifferencePerGame != test.alphaPerD || alpha.NPXGSortTotal != test.alphaSortTotal {
				t.Fatalf("alpha NPxG = %q (%q) diff %q (%q) sort %q", alpha.NPXGForAgainst, alpha.NPXGForAgainstPerGame, alpha.NPXGDifference, alpha.NPXGDifferencePerGame, alpha.NPXGSortTotal)
			}
			if bravo.NPXGForAgainst != test.bravoNP {
				t.Fatalf("bravo NPxG = %q, want %q", bravo.NPXGForAgainst, test.bravoNP)
			}
		})
	}
}

func TestAddXGValuesClampsPenaltyXGAboveGameXG(t *testing.T) {
	data := npxgSeasonData(&cache.PenaltySplit{HomeXG: 2.3601}, &cache.PenaltySplit{})
	rows, _ := addXGValues([]tableRowView{{TeamID: "alpha"}, {TeamID: "bravo"}}, data)
	if got := rowFor(rows, "alpha").NPXGForAgainst; got != "0.64/2.11" {
		t.Fatalf("alpha NPxG = %q, want the over-penalized side clamped at zero", got)
	}
}

func renderStandings(t *testing.T, data cache.SeasonData) string {
	t.Helper()
	response := httptest.NewRecorder()
	NewHandlerWithOptions(fakeStore{season: data}, Options{Rules: testRules(30), Location: time.UTC}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/seasons/2026/regular-season", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	return response.Body.String()
}

func TestStandingsRendersNPXGControlsAndAttributes(t *testing.T) {
	body := renderStandings(t, npxgSeasonData(&cache.PenaltySplit{HomeXG: .76, HomeGoals: 1, HomeShots: 1}, &cache.PenaltySplit{}))

	requireElements(t, body, `button[data-standings-stat-value=npxg][aria-pressed=false]`)
	requireText(t, body, "button[data-standings-stat-value=npxg]", "NPxG")
	requireAttr(t, body, "[data-standings-caption]", "data-npxg-label", "2026 Regular Season non-penalty xG, ordered by NPxGD")
	requireAttr(t, body, "[data-standings]", "data-standings-stat", "goals")
	requireElements(t, body,
		`td[data-standings-stat-column=xg][data-total="3.00/2.11"][data-np-total="2.24/2.11"][data-np-per-game="1.12/1.06"]`,
		`td[data-standings-np-difference][data-np-total="+0.13"][data-np-per-game="+0.06"][data-np-sort-total="0.1300"]`,
		`th[data-standings-header][data-npxg-label="NPxG +/-"][data-npxg-title="Non-penalty expected goals for / against"]`,
		`th[data-standings-header][data-npxg-label=NPxGD][data-npxg-title="Non-penalty expected-goal difference"]`,
	)
	// The points column keeps the official values for NPxG mode; only xG mode
	// swaps in xPts.
	requireAttr(t, body, "th[data-standings-points-label]", "data-goals-label", "Pts")
	forbidRaw(t, body, "Incomplete NPxG data", "data-standings-stat-warning")
}

func TestStandingsWarnsWhenNPXGCoverageIsBelowXGCoverage(t *testing.T) {
	body := renderStandings(t, npxgSeasonData(&cache.PenaltySplit{HomeXG: .76, HomeGoals: 1, HomeShots: 1}, nil))

	requireElements(t, body, `p[data-standings-stat-warning=npxg][hidden]`)
	requireText(t, body, "p[data-standings-stat-warning=npxg]", "Non-penalty xG is unavailable for 1 of 2 completed matches, so the NPxG columns are incomplete.")
	requireElements(t, body, `td[data-np-total="—"]`)
	forbidRaw(t, body, "Incomplete xG data:")
}

func TestStandingsHidesNPXGWithoutAnySplit(t *testing.T) {
	body := renderStandings(t, npxgSeasonData(nil, nil))

	forbidElements(t, body, `button[data-standings-stat-value=npxg]`, `[data-standings-stat-warning]`, `td[data-np-total]`, `th[data-npxg-label]`)
	forbidRaw(t, body, "data-npxg-label", "NPxG")
	requireElements(t, body, `button[data-standings-stat-value=xg]`)
}
