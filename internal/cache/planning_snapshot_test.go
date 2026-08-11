package cache

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestPlanningSnapshotIsConsistentAndDefensive(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir()+"/planning.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	now := time.Date(2034, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := db.EnsureSourceScopes(ctx, "2034", "Regular Season", now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpsertTeams(ctx, []Team{{ASAID: "home", Name: "Home"}, {ASAID: "away", Name: "Away"}}, FullRefreshMetadata{Trigger: SourceTriggerScheduler, StartedAt: now, FinishedAt: now}); err != nil {
		t.Fatal(err)
	}
	game := Game{ASAID: "one", Season: "2034", Stage: "Regular Season", KickoffUTC: "2034-01-01T00:00:00Z", Status: "FullTime", HomeTeamID: "home", AwayTeamID: "away", HomeScore: sql.NullInt64{Int64: 1, Valid: true}, AwayScore: sql.NullInt64{Valid: true}, LastUpdatedUTC: "2034-01-01T00:00:00Z", RawJSON: `{}`}
	if _, err := db.ReplaceGameInventory(ctx, "2034", "Regular Season", []Game{game}, nil, FullRefreshMetadata{Trigger: SourceTriggerScheduler, StartedAt: now, FinishedAt: now, NextFullDueAt: cachePlanningTimePointer(now.Add(time.Hour))}); err != nil {
		t.Fatal(err)
	}
	due := now.Add(2 * time.Hour)
	if _, err := db.UpsertCheckedXG(ctx, "2034", "Regular Season", []CheckedXGRequest{{GameID: "one", NextDueAt: &due}}, []GameXG{}, TargetedRefreshMetadata{Trigger: SourceTriggerScheduler, StartedAt: now, FinishedAt: now}); err != nil {
		t.Fatal(err)
	}

	snapshot, err := db.PlanningSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var scope *PlanningScopeSnapshot
	for i := range snapshot.Scopes {
		if snapshot.Scopes[i].Readiness.Scope.Season == "2034" && snapshot.Scopes[i].Readiness.Scope.Stage == "Regular Season" {
			scope = &snapshot.Scopes[i]
			break
		}
	}
	if scope == nil || scope.GamesFull == nil || len(scope.Games) != 1 || len(scope.ResultChecks) != 1 || len(scope.XGChecks) != 1 {
		t.Fatalf("planning scope = %+v", scope)
	}
	scope.Games[0].Status = "bad"
	*scope.XGChecks[0].NextDueAt = time.Time{}
	again, err := db.PlanningSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range again.Scopes {
		if candidate.Readiness.Scope.Season == "2034" && candidate.Readiness.Scope.Stage == "Regular Season" {
			if candidate.Games[0].Status != "FullTime" || candidate.XGChecks[0].NextDueAt == nil || !candidate.XGChecks[0].NextDueAt.Equal(due) {
				t.Fatalf("snapshot was not defensive: %+v", candidate)
			}
			return
		}
	}
	t.Fatal("re-read scope missing")
}

func cachePlanningTimePointer(value time.Time) *time.Time { return &value }
