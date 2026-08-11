package cache

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestCheckedWritesSelectMaterialDueAtomically(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir()+"/due.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	start := time.Date(2034, 2, 1, 0, 0, 0, 0, time.UTC)
	if _, err := db.UpsertTeams(ctx, []Team{{ASAID: "home", Name: "Home"}, {ASAID: "away", Name: "Away"}}, FullRefreshMetadata{Trigger: SourceTriggerScheduler, StartedAt: start, FinishedAt: start}); err != nil {
		t.Fatal(err)
	}
	game := Game{ASAID: "one", Season: "2034", Stage: "Regular Season", KickoffUTC: "2034-02-01T00:00:00Z", Status: "PreMatch", HomeTeamID: "home", AwayTeamID: "away", LastUpdatedUTC: "2034-02-01T00:00:00Z", RawJSON: `{}`}
	if _, err := db.ReplaceGameInventory(ctx, "2034", "Regular Season", []Game{game}, nil, FullRefreshMetadata{Trigger: SourceTriggerScheduler, StartedAt: start, FinishedAt: start}); err != nil {
		t.Fatal(err)
	}
	finish := start.Add(time.Hour)
	normal := finish.Add(24 * time.Hour)
	material := finish.Add(6 * time.Hour)
	completed := game
	completed.Status = "FullTime"
	completed.HomeScore = sql.NullInt64{Int64: 1, Valid: true}
	completed.AwayScore = sql.NullInt64{Valid: true}
	completed.LastUpdatedUTC = "2034-02-01T01:00:00Z"
	if _, err := db.UpsertCheckedGames(ctx, "2034", "Regular Season", []CheckedGameRequest{{ASAID: "one", NextDueAt: &normal, MaterialNextDueAt: &material}}, []Game{completed}, TargetedRefreshMetadata{Trigger: SourceTriggerScheduler, StartedAt: finish, FinishedAt: finish}); err != nil {
		t.Fatal(err)
	}
	state, ok, err := db.GameResultCheckState(ctx, "one")
	if err != nil || !ok || state.NextDueAt == nil || !state.NextDueAt.Equal(material) {
		t.Fatalf("terminal material state=%+v ok=%t err=%v", state, ok, err)
	}
	finish = finish.Add(time.Hour)
	normal = finish.Add(24 * time.Hour)
	material = finish.Add(6 * time.Hour)
	if _, err := db.UpsertCheckedGames(ctx, "2034", "Regular Season", []CheckedGameRequest{{ASAID: "one", NextDueAt: &normal, MaterialNextDueAt: &material}}, []Game{completed}, TargetedRefreshMetadata{Trigger: SourceTriggerScheduler, StartedAt: finish, FinishedAt: finish}); err != nil {
		t.Fatal(err)
	}
	state, _, _ = db.GameResultCheckState(ctx, "one")
	if state.NextDueAt == nil || !state.NextDueAt.Equal(normal) {
		t.Fatalf("identical state=%+v", state)
	}
	available := GameXG{GameID: "one", Availability: XGAvailable, HomeTeamID: "home", AwayTeamID: "away", HomeXG: sql.NullFloat64{Float64: 1, Valid: true}, AwayXG: sql.NullFloat64{Float64: 1, Valid: true}, RawJSON: `{}`}
	if _, err := db.UpsertCheckedXG(ctx, "2034", "Regular Season", []CheckedXGRequest{{GameID: "one", NextDueAt: &normal, MaterialNextDueAt: &material}}, []GameXG{available}, TargetedRefreshMetadata{Trigger: SourceTriggerScheduler, StartedAt: finish, FinishedAt: finish}); err != nil {
		t.Fatal(err)
	}
	xg, ok, err := db.GameXGCheckState(ctx, "one")
	if err != nil || !ok || xg.NextDueAt == nil || !xg.NextDueAt.Equal(material) {
		t.Fatalf("available xG state=%+v ok=%t err=%v", xg, ok, err)
	}
	finish = finish.Add(time.Hour)
	normal, material = finish.Add(24*time.Hour), finish.Add(6*time.Hour)
	if _, err := db.UpsertCheckedXG(ctx, "2034", "Regular Season", []CheckedXGRequest{{GameID: "one", NextDueAt: &normal, MaterialNextDueAt: &material}}, []GameXG{available}, TargetedRefreshMetadata{Trigger: SourceTriggerScheduler, StartedAt: finish, FinishedAt: finish}); err != nil {
		t.Fatal(err)
	}
	xg, _, _ = db.GameXGCheckState(ctx, "one")
	if xg.NextDueAt == nil || !xg.NextDueAt.Equal(normal) {
		t.Fatalf("identical xG state=%+v", xg)
	}
	finish = finish.Add(time.Hour)
	normal, material = finish.Add(24*time.Hour), finish.Add(6*time.Hour)
	available.HomeXG.Float64 = 2
	if _, err := db.UpsertCheckedXG(ctx, "2034", "Regular Season", []CheckedXGRequest{{GameID: "one", NextDueAt: &normal, MaterialNextDueAt: &material}}, []GameXG{available}, TargetedRefreshMetadata{Trigger: SourceTriggerScheduler, StartedAt: finish, FinishedAt: finish}); err != nil {
		t.Fatal(err)
	}
	xg, _, _ = db.GameXGCheckState(ctx, "one")
	if xg.NextDueAt == nil || !xg.NextDueAt.Equal(material) {
		t.Fatalf("material xG state=%+v", xg)
	}
}
