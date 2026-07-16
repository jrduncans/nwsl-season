package cache

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/clinching"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/scenarios"
)

func TestFixtureSnapshotIDIsOrderIndependentAndScoreSensitive(t *testing.T) {
	teams := []Team{{ASAID: "a"}, {ASAID: "b"}, {ASAID: "unused"}}
	games := []Game{{ASAID: "two", Status: "PreMatch", HomeTeamID: "b", AwayTeamID: "a", KickoffUTC: "2026-02-02T00:00:00Z"}, {ASAID: "one", Status: "FullTime", HomeTeamID: "a", AwayTeamID: "b", KickoffUTC: "2026-02-01T00:00:00Z", HomeScore: sql.NullInt64{Int64: 1, Valid: true}, AwayScore: sql.NullInt64{Valid: true}}}
	first, err := FixtureSnapshotID(teams, games)
	if err != nil {
		t.Fatal(err)
	}
	second, err := FixtureSnapshotID([]Team{teams[2], teams[1], teams[0]}, []Game{games[1], games[0]})
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("hash varies with order: %s %s", first, second)
	}
	games[0].Status = "FullTime"
	games[0].HomeScore = sql.NullInt64{Int64: 2, Valid: true}
	games[0].AwayScore = sql.NullInt64{Valid: true}
	changed, _ := FixtureSnapshotID(teams, games)
	if changed == first {
		t.Fatal("hash did not include fixture state")
	}
}

func TestScenarioRoundTripUsesExactCurrentQualificationSnapshot(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir()+"/cache.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	teams := []Team{{ASAID: "a", Name: "A"}, {ASAID: "b", Name: "B"}}
	games := []Game{{ASAID: "g", Season: "2026", Stage: "Regular Season", KickoffUTC: "2026-01-01T00:00:00Z", Status: "PreMatch", HomeTeamID: "a", AwayTeamID: "b"}}
	syncRun, err := db.ReplaceSeason(ctx, "2026", "Regular Season", teams, games, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	qualification, err := db.ReplaceQualification(ctx, QualificationRun{FixtureSnapshotID: syncRun.FixtureSnapshotID, SourceSyncRunID: syncRun.ID, Season: "2026", Stage: "Regular Season", RulesVersion: "test-v1", ExpectedStatuses: 1, WrittenStatuses: 1}, []QualificationStatus{{TeamID: "a", Achievement: competition.AchievementPlayoffs, TopK: 1, Status: clinching.Clinched, Method: clinching.ProofCheapBound, StrictlyAhead: clinching.CountEvidence{Kind: "upper_bound"}, AtLeastLevel: clinching.CountEvidence{Kind: "upper_bound"}, BlockingWitness: []clinching.WitnessGame{}, FrontierWitness: []clinching.WitnessGame{}, NoHelp: clinching.NoHelpPath{State: clinching.NoHelpNotApplicable, FixtureIDs: []string{}}}})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	slate := scenarios.Slate{ID: "slate", DefinitionVersion: scenarios.DefinitionVersion, State: scenarios.SlateReady, Source: scenarios.SourceMatchday, Matchday: 1, StartsAtUTC: start, LatestKickoffUTC: start, CutoffUTC: start, FixtureIDs: []string{"g"}}
	result := ScenarioResult{Result: scenarios.Result{TeamID: "a", Achievement: competition.AchievementPlayoffs, TopK: 1, State: scenarios.OpportunityAlreadyClinched, AlreadyClinched: true, Clauses: []scenarios.Clause{}, Necessary: []scenarios.FixtureCondition{}, ProofMethods: []clinching.ProofMethod{}}}
	if _, err := db.ReplaceScenario(ctx, ScenarioRun{FixtureSnapshotID: syncRun.FixtureSnapshotID, QualificationRunID: qualification.Run.ID, SourceSyncRunID: syncRun.ID, Season: "2026", Stage: "Regular Season", RulesVersion: "test-v1", DefinitionVersion: scenarios.DefinitionVersion, Slate: slate, ExpectedResults: 1, WrittenResults: 1}, []ScenarioResult{result}); err != nil {
		t.Fatal(err)
	}
	snapshot, found, err := db.ScenarioForSnapshot(ctx, syncRun.FixtureSnapshotID, "test-v1", scenarios.DefinitionVersion)
	if err != nil || !found || len(snapshot.Results) != 1 || !snapshot.Results[0].AlreadyClinched {
		t.Fatalf("snapshot=%+v found=%v err=%v", snapshot, found, err)
	}
}

func TestQualificationRoundTrip(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir()+"/cache.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	teams := []Team{{ASAID: "a", Name: "A"}, {ASAID: "b", Name: "B"}}
	games := []Game{{ASAID: "g", Season: "2026", Stage: "Regular Season", KickoffUTC: "2026-01-01T00:00:00Z", Status: "PreMatch", HomeTeamID: "a", AwayTeamID: "b"}}
	syncRun, err := db.ReplaceSeason(ctx, "2026", "Regular Season", teams, games, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	row := QualificationStatus{TeamID: "a", Achievement: competition.AchievementPlayoffs, TopK: 1, Status: clinching.Clinched, Method: clinching.ProofCheapBound, StrictlyAhead: clinching.CountEvidence{Value: 0, Kind: "upper_bound"}, AtLeastLevel: clinching.CountEvidence{Value: 0, Kind: "upper_bound"}, BlockingWitness: []clinching.WitnessGame{}, FrontierWitness: []clinching.WitnessGame{}, NoHelp: clinching.NoHelpPath{State: clinching.NoHelpNotApplicable, FixtureIDs: []string{}}}
	run := QualificationRun{FixtureSnapshotID: syncRun.FixtureSnapshotID, SourceSyncRunID: syncRun.ID, Season: "2026", Stage: "Regular Season", RulesVersion: "test-v1", ExpectedStatuses: 1, WrittenStatuses: 1}
	if _, err := db.ReplaceQualification(ctx, run, []QualificationStatus{row}); err != nil {
		t.Fatal(err)
	}
	snapshot, ok, err := db.QualificationForSnapshot(ctx, syncRun.FixtureSnapshotID, "test-v1")
	if err != nil || !ok || len(snapshot.Statuses) != 1 || snapshot.Statuses[0].TeamID != "a" {
		t.Fatalf("snapshot=%+v ok=%v err=%v", snapshot, ok, err)
	}
}
