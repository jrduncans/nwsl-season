package app

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
)

type handoffStore struct {
	seasonArchiveStore
	regular, playoffs cache.SeasonData
}

func (s handoffStore) Season(_ context.Context, season, stage string) (cache.SeasonData, error) {
	if season != "2026" {
		return cache.SeasonData{}, nil
	}
	if stage == "Regular Season" {
		return s.regular, nil
	}
	return s.playoffs, nil
}

func resolvedHandoffSeason() cache.SeasonData {
	data := completeCatalogSeasonData()
	// Distinct goal differences resolve all ranks even with equal points.
	for i := range data.Games {
		data.Games[i].HomeScore.Int64 = int64(i%len(data.Teams) + 1)
		data.Games[i].KickoffUTC = fmt.Sprintf("2026-01-01T%02d:%02d:00Z", i/60, i%60)
	}
	return data
}

func handoffResponse(t *testing.T, store handoffStore, route string) string {
	t.Helper()
	w := httptest.NewRecorder()
	NewHandler(store).ServeHTTP(w, httptest.NewRequest(http.MethodGet, route, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("%s = %d: %s", route, w.Code, w.Body.String())
	}
	return w.Body.String()
}

func TestPlayoffHandoffTransitionAndPublicationStates(t *testing.T) {
	store := handoffStore{regular: resolvedHandoffSeason()}
	store.readiness = []cache.SeasonReadinessSnapshot{{Scope: cache.SourceScope{Season: "2026", Stage: "Playoffs"}, Readiness: cache.SourceReadinessNotPublished}}
	final := store.regular.Games[0]
	store.regular.Games[0].Status = fixtures.PreMatchStatus
	store.regular.Games[0].HomeScore, store.regular.Games[0].AwayScore = sql.NullInt64{}, sql.NullInt64{}
	body := handoffResponse(t, store, "/seasons/2026/regular-season")
	forbidElements(t, body, ".season-handoff", `[data-stage-destination="playoffs"]`)
	store.regular.Games[0] = final
	body = handoffResponse(t, store, "/seasons/2026/regular-season")
	requireElements(t, body, `.season-handoff a[href="playoffs"]`, `[data-stage-destination="playoffs"]`, ".standings")
	requireText(t, body, "main", "Final standings", "regular-season results")
	seeds, err := (&application{store: store}).finalPlayoffSeeds(context.Background(), "2026")
	if err != nil || len(seeds.Seeds) != 16 {
		t.Fatalf("final seeds = %+v, %v", seeds, err)
	}
	for _, tc := range []struct {
		name      string
		games     []cache.Game
		published bool
	}{
		{name: "empty"},
		{name: "placeholder", games: []cache.Game{{ASAID: "placeholder"}}},
		{name: "partial", games: []cache.Game{{ASAID: "fourth-quarterfinal", Season: "2026", Stage: "Playoffs", HomeTeamID: seeds.Seeds[3], AwayTeamID: seeds.Seeds[6], KickoffUTC: "2026-11-01T20:00:00Z", Status: fixtures.PreMatchStatus}}, published: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store.playoffs = cache.SeasonData{Games: tc.games}
			body := handoffResponse(t, store, "/seasons/2026/playoffs")
			requireElements(t, body, "[data-bracket-state]", `a[href="regular-season"]`)
			requireText(t, body, "main", "Expected from final seeds", "not published matches", "publication or discovery", "Date and venue TBD", "Seeds 3 and 6")
			if strings.Contains(body, "Published by ASA") != tc.published {
				t.Fatal("source and expected match labels were not distinct")
			}
			if strings.Contains(body, "ASA has not published") {
				t.Fatal("cache absence asserted ASA nonpublication")
			}
		})
	}
	store.status.LastAttempt = &cache.SyncRun{Outcome: "failure"}
	body = handoffResponse(t, store, "/seasons/2026/playoffs")
	requireText(t, body, "main", "latest refresh did not succeed")

	// Published results replace expectations and advance through the same URL,
	// even when quarterfinal kickoff order differs from the bracket topology.
	store.status.LastAttempt = nil
	store.playoffs.Games = nil
	for i, pair := range [][2]int{{3, 6}, {4, 5}, {2, 7}, {1, 8}, {2, 3}, {1, 4}, {1, 2}} {
		store.playoffs.Games = append(store.playoffs.Games, cache.Game{
			ASAID: fmt.Sprintf("published-%d", i), Season: "2026", Stage: "Playoffs",
			HomeTeamID: seeds.Seeds[pair[0]], AwayTeamID: seeds.Seeds[pair[1]],
			Status: fixtures.CompletedStatus, KickoffUTC: fmt.Sprintf("2026-11-%02dT20:00:00Z", i+1),
			HomeScore: sql.NullInt64{Int64: 2, Valid: true}, AwayScore: sql.NullInt64{Int64: 0, Valid: true},
		})
	}
	body = handoffResponse(t, store, "/seasons/2026/playoffs")
	requireElements(t, body, `[data-bracket-state="ready"]`)
	requireText(t, body, "main", "ASA-published matches and results", "2–0", "2026-11-07T20:00:00Z")
	if strings.Count(body, "Published by ASA") != 7 || strings.Contains(body, "Expected from final seeds") {
		t.Fatal("published matches did not replace expectations")
	}
	store.playoffs.Games[0].AwayTeamID = seeds.Seeds[8]
	body = handoffResponse(t, store, "/seasons/2026/playoffs")
	requireElements(t, body, `[data-bracket-state="format_mismatch"]`, `a[href="playoffs/fixtures"]`, `[aria-label="Published matches"] .fixtures`)
	requireText(t, body, "main", "may disagree with the expected bracket")
}

func TestPlayoffSeedsRequireSafeFinalityAndResolvedOfficialRanks(t *testing.T) {
	for _, kind := range []string{"penultimate", "incomplete", "abandoned", "unknown", "missing-score", "tied", "resolved"} {
		t.Run(kind, func(t *testing.T) {
			store := handoffStore{regular: resolvedHandoffSeason()}
			switch kind {
			case "penultimate":
				store.regular.Games[0].Status = fixtures.PreMatchStatus
			case "incomplete":
				store.regular.Games = store.regular.Games[1:]
			case "abandoned":
				store.regular.Games[0].Status = fixtures.AbandonedStatus
			case "unknown":
				store.regular.Games[0].Status = "Delayed"
			case "missing-score":
				store.regular.Games[0].HomeScore = sql.NullInt64{}
			case "tied":
				store.regular = completeCatalogSeasonData()
			}
			result, err := (&application{store: store}).finalPlayoffSeeds(context.Background(), "2026")
			if err != nil {
				t.Fatal(err)
			}
			wantComplete := kind == "resolved" || kind == "tied"
			if result.Complete != wantComplete || (len(result.Seeds) > 0) != (kind == "resolved") {
				t.Fatalf("%s seeds = %+v", kind, result)
			}
		})
	}
}

func TestPublishedMatchWithUnresolvedSeedsIsNotAMismatch(t *testing.T) {
	// Every rank is tied, so no seed resolves; a published quarterfinal fits
	// several seed slots and stays listed rather than placed or flagged.
	store := handoffStore{regular: completeCatalogSeasonData()}
	teams := store.regular.Teams
	store.playoffs = cache.SeasonData{Games: []cache.Game{{ASAID: "quarterfinal", Season: "2026", Stage: "Playoffs", HomeTeamID: teams[0].ID, AwayTeamID: teams[1].ID, KickoffUTC: "2026-11-01T20:00:00Z", Status: fixtures.PreMatchStatus}}}
	body := handoffResponse(t, store, "/seasons/2026/playoffs")
	requireElements(t, body, `[data-bracket-state="unresolved"]`, `[aria-label="Published matches"] .fixtures`, `a[href="regular-season"]`)
	forbidText(t, body, "main", "Bracket format mismatch", "may disagree with the expected bracket")
}
