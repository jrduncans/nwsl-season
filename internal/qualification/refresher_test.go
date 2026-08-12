package qualification

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/clinching"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/standings"
	"github.com/jrduncans/nwsl-season/internal/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestCalculationTelemetrySummarizesFastLoopWork(t *testing.T) {
	summary := calculationTelemetry{}
	summary.recordStatusProof(4*time.Millisecond, "fast-team", competition.Achievement{ID: competition.AchievementPlayoffs})
	summary.recordStatusProof(telemetry.SlowOperationThreshold, "slow-team", competition.Achievement{ID: competition.AchievementShield})
	summary.recordNoHelpBatch(telemetry.SlowOperationThreshold, "slow-team")
	summary.recordStatusProofDiagnostics(clinching.AchievementResult{Diagnostics: clinching.Diagnostics{
		ReducedTeams: 2, ReducedFixtures: 3, ConnectedComponents: 1, SubsetProbes: 4, VisitedStates: 5, MemoHits: 6, TotalPrunes: 7,
	}})
	summary.skippedStatusProofs = 1
	summary.skippedNoHelpBatches = 2

	statuses := []cache.QualificationStatus{
		{Status: clinching.NotClinched, Method: clinching.ProofCheapBound, NoHelp: clinching.NoHelpPath{State: clinching.NoHelpGuaranteed}},
		{Status: clinching.Unresolved, Method: clinching.ProofComputeBudget, NoHelp: clinching.NoHelpPath{State: clinching.NoHelpUnresolved, Reason: "calculation budget exhausted"}},
	}
	attributes := qualificationAttributeMap(summary.attributes(statuses))
	for key, want := range map[string]int64{
		"nwsl.qualification.status_check_count":                         2,
		"nwsl.qualification.status_proof.skipped_count":                 1,
		"nwsl.qualification.status_proof.slow_count":                    1,
		"nwsl.qualification.status_proof.reduced_team_count.max":        2,
		"nwsl.qualification.status_proof.reduced_fixture_count.max":     3,
		"nwsl.qualification.status_proof.connected_component_count.max": 1,
		"nwsl.qualification.status_proof.subset_probe_count.total":      4,
		"nwsl.qualification.status_proof.visited_state_count.total":     5,
		"nwsl.qualification.status_proof.memo_hit_count.total":          6,
		"nwsl.qualification.status_proof.prune_count.total":             7,
		"nwsl.qualification.no_help_batch_count":                        1,
		"nwsl.qualification.no_help_batch.skipped_count":                2,
		"nwsl.qualification.no_help_batch.slow_count":                   1,
		"nwsl.qualification.result.status.not_clinched_count":           1,
		"nwsl.qualification.result.status.unresolved_count":             1,
		"nwsl.qualification.result.method.cheap_bound_count":            1,
		"nwsl.qualification.result.method.compute_budget_count":         1,
		"nwsl.qualification.result.no_help.guaranteed_count":            1,
		"nwsl.qualification.result.no_help.unresolved_count":            1,
		"nwsl.qualification.result.budget_exhausted_count":              1,
	} {
		if got := attributes[key].AsInt64(); got != want {
			t.Errorf("%s = %d, want %d", key, got, want)
		}
	}
	if got := attributes["nwsl.qualification.status_proof.slowest_team_id"].AsString(); got != "slow-team" {
		t.Errorf("slowest status-proof team = %q, want slow-team", got)
	}
	if got := attributes["nwsl.qualification.no_help_batch.slowest_team_id"].AsString(); got != "slow-team" {
		t.Errorf("slowest no-help team = %q, want slow-team", got)
	}
}

func TestCalculateAddsTelemetryToRefreshSpan(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := trace.NewTracerProvider(trace.WithSyncer(exporter))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		_ = provider.Shutdown(context.Background())
	})

	ctx, span := telemetry.Tracer().Start(context.Background(), "qualification.refresh")
	_, _ = (Refresher{}).calculate(ctx, nil, nil)
	span.End()

	spans := exporter.GetSpans()
	if len(spans) != 1 || spans[0].Name != "qualification.refresh" {
		t.Fatalf("spans = %#v, want only qualification.refresh", spans)
	}
	attributes := qualificationAttributeMap(spans[0].Attributes)
	if got := attributes["nwsl.qualification.input_team_count"].AsInt64(); got != 0 {
		t.Errorf("nwsl.qualification.input_team_count = %d, want 0", got)
	}
	if got := attributes["nwsl.qualification.status_check_count"].AsInt64(); got != 0 {
		t.Errorf("nwsl.qualification.status_check_count = %d, want 0", got)
	}
}

func TestCurrentRefreshDoesNotEmitChildSpan(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := trace.NewTracerProvider(trace.WithSyncer(exporter))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		_ = provider.Shutdown(context.Background())
	})

	rules := competition.Rules{
		Season: "2026", Stage: "Regular Season", Version: "rules-v1",
		ExpectedTeams: 1, GamesPerTeam: 1,
		Achievements: []competition.Achievement{{ID: competition.AchievementShield, Label: "Shield", TopK: 1}},
	}
	ctx, parent := telemetry.Tracer().Start(context.Background(), "sync.recalculate")
	result, err := (Refresher{Store: currentQualificationStore{}, Rules: rules}).Refresh(ctx, cache.SyncRun{Season: rules.Season, Stage: rules.Stage, FixtureSnapshotID: "fixture-1"}, nil, nil, false)
	parent.End()
	if err != nil {
		t.Fatal(err)
	}
	if result.Recalculated || result.Required || result.Reason != "snapshot_current" {
		t.Fatalf("refresh result = %+v, want current no-op", result)
	}
	spans := exporter.GetSpans()
	if len(spans) != 1 || spans[0].Name != "sync.recalculate" {
		t.Fatalf("spans = %#v, want only parent sync.recalculate", spans)
	}
}

type currentQualificationStore struct{}

func (currentQualificationStore) QualificationForSnapshot(context.Context, string, string) (cache.QualificationSnapshot, bool, error) {
	return cache.QualificationSnapshot{}, true, nil
}

func (currentQualificationStore) ReplaceQualification(context.Context, cache.QualificationRun, []cache.QualificationStatus) (cache.QualificationSnapshot, error) {
	return cache.QualificationSnapshot{}, nil
}

func (currentQualificationStore) RecordQualificationFailure(context.Context, cache.QualificationRun, error) error {
	return nil
}

func qualificationAttributeMap(values []attribute.KeyValue) map[string]attribute.Value {
	attributes := make(map[string]attribute.Value, len(values))
	for _, value := range values {
		attributes[string(value.Key)] = value.Value
	}
	return attributes
}

func TestShouldRetryLegacyKickoffOrderBatch(t *testing.T) {
	snapshot := cache.QualificationSnapshot{Statuses: []cache.QualificationStatus{{Method: clinching.ProofIncompleteSchedule, Reason: "fixture kickoff order is invalid"}, {Method: clinching.ProofIncompleteSchedule, Reason: "fixture kickoff order is invalid"}}}
	games := []cache.Game{{ASAID: "g1", Status: "PreMatch", HomeTeamID: "a", AwayTeamID: "b", KickoffUTC: "2026-11-01 22:00:00 UTC"}, {ASAID: "g2", Status: "FullTime", HomeTeamID: "b", AwayTeamID: "a", KickoffUTC: "2026-10-01 22:00:00 UTC", HomeScore: sql.NullInt64{Valid: true}, AwayScore: sql.NullInt64{Valid: true}}}
	if !shouldRetryKickoffOrder(snapshot, games) {
		t.Fatal("expected legacy kickoff-order batch to be retried")
	}

	snapshot.Statuses[1].Achievement = competition.AchievementPlayoffs
	if !shouldRetryKickoffOrder(snapshot, games) {
		t.Fatal("achievement metadata should not affect retry detection")
	}
}

func TestShouldRetryComputeBudgetBatch(t *testing.T) {
	tests := []struct {
		name     string
		status   cache.QualificationStatus
		expected bool
	}{
		{name: "status proof exhausted", status: cache.QualificationStatus{Method: clinching.ProofComputeBudget}, expected: true},
		{name: "no-help proof exhausted", status: cache.QualificationStatus{Method: clinching.ProofCheapBound, NoHelp: clinching.NoHelpPath{State: clinching.NoHelpUnresolved, Reason: "calculation budget exhausted"}}, expected: true},
		{name: "other no-help limitation", status: cache.QualificationStatus{Method: clinching.ProofCheapBound, NoHelp: clinching.NoHelpPath{State: clinching.NoHelpUnresolved, Reason: "missing tiebreak data"}}, expected: false},
		{name: "completed no-help proof", status: cache.QualificationStatus{Method: clinching.ProofCheapBound, NoHelp: clinching.NoHelpPath{State: clinching.NoHelpGuaranteed}}, expected: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := shouldRetryComputeBudget(cache.QualificationSnapshot{Statuses: []cache.QualificationStatus{test.status}})
			if got != test.expected {
				t.Fatalf("shouldRetryComputeBudget() = %t, want %t", got, test.expected)
			}
		})
	}
}

func TestCompleteInventoryRequiresEveryDoubleRoundRobinFixture(t *testing.T) {
	teams := []standings.Team{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}}
	rules := competition.Rules{ExpectedTeams: 4, GamesPerTeam: 6}
	games := []standings.Game{}
	for _, home := range teams {
		for _, away := range teams {
			if home.ID == away.ID {
				continue
			}
			games = append(games, standings.Game{
				ID: fmt.Sprintf("%s-%s", home.ID, away.ID), HomeTeamID: home.ID, AwayTeamID: away.ID,
			})
		}
	}
	if !completeInventory(rules, teams, games) {
		t.Fatal("valid double round robin was rejected")
	}

	corrupted := append([]standings.Game(nil), games...)
	for i := range corrupted {
		switch corrupted[i].ID {
		case "a-b":
			corrupted[i].HomeTeamID, corrupted[i].AwayTeamID = "a", "c"
		case "c-d":
			corrupted[i].HomeTeamID, corrupted[i].AwayTeamID = "b", "d"
		}
	}
	counts := map[string]int{}
	for _, game := range corrupted {
		counts[game.HomeTeamID]++
		counts[game.AwayTeamID]++
	}
	for _, team := range teams {
		if counts[team.ID] != rules.GamesPerTeam {
			t.Fatalf("test corruption changed %s's fixture count to %d", team.ID, counts[team.ID])
		}
	}
	if completeInventory(rules, teams, corrupted) {
		t.Fatal("degree-correct fixture duplication was accepted")
	}
}
