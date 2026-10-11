package syncer

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/asa"
	"github.com/jrduncans/nwsl-season/internal/asatest"
	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/telemetry/nwslconv"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

const penaltySeason, penaltyStage = "2026", "Regular Season"

type penaltyLogExporter struct{ records []sdklog.Record }

func (e *penaltyLogExporter) Export(_ context.Context, records []sdklog.Record) error {
	e.records = append(e.records, records...)
	return nil
}
func (*penaltyLogExporter) Shutdown(context.Context) error   { return nil }
func (*penaltyLogExporter) ForceFlush(context.Context) error { return nil }

func captureLogs(t *testing.T) *penaltyLogExporter {
	t.Helper()
	exporter := &penaltyLogExporter{}
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(exporter)))
	previous := otel.GetLoggerProvider()
	otel.SetLoggerProvider(provider)
	t.Cleanup(func() {
		otel.SetLoggerProvider(previous)
		_ = provider.Shutdown(context.Background())
	})
	return exporter
}

func (e *penaltyLogExporter) penaltyWarnings() int {
	count := 0
	for _, record := range e.records {
		if record.Severity() != otellog.SeverityWarn {
			continue
		}
		record.WalkAttributes(func(value attribute.KeyValue) bool {
			if value.Key == nwslconv.ErrorCodeKey && value.Value.AsString() == nwslconv.ErrorCodeSyncPenaltyXG {
				count++
			}
			return true
		})
	}
	return count
}

// penaltyFixture caches a two-team, two-game season (both games finished 1-0
// with xG) from the fake ASA server, leaving the fake's request log empty.
func penaltyFixture(t *testing.T) (context.Context, *asatest.Server, *cache.DB, Service, *asatest.Scenario) {
	t.Helper()
	ctx := context.Background()
	base := time.Date(2026, time.March, 14, 18, 0, 0, 0, time.UTC)
	scenario := asatest.Season(2, base).
		PlayThrough(base.AddDate(1, 0, 0), func(asa.Game) (int, int) { return 1, 0 }).
		WithXG(1).
		WithPenalty("game-001", asatest.PenaltyPattern{HomeXG: .76, HomeGoals: 1, HomeShots: 1})
	fake := asatest.New(t)
	fake.Load(scenario)
	db := newTestDB(t)
	clock := time.Date(2026, time.June, 1, 0, 0, 0, 0, time.UTC)
	service := Service{ASA: fake.Client(), Store: db, Now: func() time.Time {
		clock = clock.Add(time.Minute)
		return clock
	}}
	for _, operation := range []Operation{
		{Resource: OperationTeams, Mode: OperationFull},
		{Resource: OperationGames, Mode: OperationFull, Season: penaltySeason, Stage: penaltyStage},
	} {
		operation.Trigger = cache.SourceTriggerScheduler
		if _, err := service.Execute(ctx, operation); err != nil {
			t.Fatal(err)
		}
	}
	fake.ResetRequests()
	return ctx, fake, db, service, scenario
}

func executeXG(t *testing.T, ctx context.Context, service Service, mode OperationMode, ids ...string) OperationResult {
	t.Helper()
	operation := Operation{Resource: OperationGameXG, Mode: mode, Season: penaltySeason, Stage: penaltyStage, Trigger: cache.SourceTriggerScheduler}
	for _, id := range ids {
		operation.Requested = append(operation.Requested, OperationGameRequest{GameID: id})
	}
	result, err := service.Execute(ctx, operation)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func xgStates(t *testing.T, ctx context.Context, db *cache.DB) map[string]cache.GameXG {
	t.Helper()
	states, err := db.GameXGStates(ctx, penaltySeason, penaltyStage)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]cache.GameXG{}
	for _, state := range states {
		out[state.GameID] = state
	}
	return out
}

func TestFullXGOperationStoresPenaltySplitsWithTwoRequests(t *testing.T) {
	ctx, fake, db, service, _ := penaltyFixture(t)
	result := executeXG(t, ctx, service, OperationFull)

	requests := fake.Requests()
	if len(requests) != 2 || requests[0].Path != asatest.PathXGoals || requests[1].Path != asatest.PathTeamXGoals {
		t.Fatalf("requests = %+v, want games/xgoals then teams/xgoals", requests)
	}
	query := requests[1].Query
	if query.Get("split_by_games") != "true" || query.Get("shot_pattern") != "Penalty" || query.Get("season_name") != penaltySeason || query.Get("stage_name") != penaltyStage || query.Has("game_id") || query.Has("start_date") || query.Has("end_date") {
		t.Fatalf("penalty query = %v", query)
	}
	if !result.penalties.Requested || result.penalties.Accepted != 2 || result.penalties.Rejected != 0 {
		t.Fatalf("penalty outcome = %+v", result.penalties)
	}

	states := xgStates(t, ctx, db)
	with, without := states["game-001"].Penalties, states["game-002"].Penalties
	if with == nil || with.HomeXG != .76 || with.HomeGoals != 1 || with.HomeShots != 1 || with.AwayXG != 0 || with.AwayShots != 0 {
		t.Fatalf("game-001 split = %+v", with)
	}
	if without == nil || *without != (cache.PenaltySplit{}) {
		t.Fatalf("game-002 split = %+v, want non-nil all-zero", without)
	}
	if states["game-001"].PenaltyRawJSON == "" {
		t.Fatal("penalty raw JSON not stored")
	}
}

func TestFullXGOperationWithoutXGMakesNoPenaltyRequest(t *testing.T) {
	ctx, fake, _, service, _ := penaltyFixture(t)
	fake.SetXGoals(nil)
	executeXG(t, ctx, service, OperationFull)
	requests := fake.Requests()
	if len(requests) != 1 || requests[0].Path != asatest.PathXGoals {
		t.Fatalf("requests = %+v, want only games/xgoals", requests)
	}
}

func TestTargetedXGOperationUsesPaddedDateWindow(t *testing.T) {
	ctx, fake, db, service, _ := penaltyFixture(t)
	executeXG(t, ctx, service, OperationTargeted, "game-001")

	requests := fake.Requests()
	if len(requests) != 2 || requests[1].Path != asatest.PathTeamXGoals {
		t.Fatalf("requests = %+v", requests)
	}
	query := requests[1].Query
	if query.Get("start_date") != "2026-03-13" || query.Get("end_date") != "2026-03-15" || query.Get("stage_name") != penaltyStage || query.Has("season_name") || query.Has("game_id") {
		t.Fatalf("targeted penalty query = %v", query)
	}
	if got := requests[0].Query.Get("game_id"); got != "game-001" {
		t.Fatalf("xG game_id = %q, want game-001", got)
	}
	states := xgStates(t, ctx, db)
	if states["game-001"].Penalties == nil || states["game-001"].Penalties.HomeXG != .76 {
		t.Fatalf("game-001 split = %+v", states["game-001"].Penalties)
	}
	if _, ok := states["game-002"]; ok {
		t.Fatal("targeted operation wrote an unrequested game")
	}
}

func TestPenaltyFailureIsAbsorbedAndKeepsStoredSplit(t *testing.T) {
	ctx, fake, db, service, scenario := penaltyFixture(t)
	executeXG(t, ctx, service, OperationFull)
	before := xgStates(t, ctx, db)["game-001"]

	// A correction to the game xG arrives while the penalty endpoint is down.
	scenario.XGoals[0].HomeTeamXGoals = 1.5
	fake.Load(scenario)
	fake.FailNext(asatest.PathTeamXGoals, 500, 1)
	logs := captureLogs(t)
	result := executeXG(t, ctx, service, OperationFull)

	after := xgStates(t, ctx, db)["game-001"]
	if after.HomeXG.Float64 != 1.5 {
		t.Fatalf("xG not written: %+v", after.HomeXG)
	}
	if after.Penalties == nil || *after.Penalties != *before.Penalties || after.PenaltyRawJSON != before.PenaltyRawJSON {
		t.Fatalf("split after failure = %+v, want %+v", after.Penalties, before.Penalties)
	}
	if states := xgStates(t, ctx, db); states["game-002"].Penalties == nil {
		t.Fatal("other game lost its split")
	}
	if !result.XGInputsChanged {
		t.Fatal("xG change was not reported")
	}
	if got := logs.penaltyWarnings(); got != 1 {
		t.Fatalf("sync.penalty_xg WARN logs = %d, want 1", got)
	}
}

func TestInconsistentPenaltyPairLeavesOnlyThatGameUnchanged(t *testing.T) {
	ctx, fake, db, service, scenario := penaltyFixture(t)
	executeXG(t, ctx, service, OperationFull)
	before := xgStates(t, ctx, db)

	bad := scenario.PenaltyXGoals
	bad[0].XGoalsFor = .5 // home xgoals_for no longer matches away xgoals_against
	rows := append([]asa.TeamGamePenaltyXGoals{}, bad...)
	rows = append(rows,
		asa.TeamGamePenaltyXGoals{TeamID: "team-0", GameID: "game-002", ShotsFor: 0, ShotsAgainst: 1, XGoalsAgainst: .3, GoalsAgainst: 1},
		asa.TeamGamePenaltyXGoals{TeamID: "team-1", GameID: "game-002", ShotsFor: 1, ShotsAgainst: 0, XGoalsFor: .3, GoalsFor: 1},
	)
	fake.SetPenaltyXGoals(rows)
	logs := captureLogs(t)
	result := executeXG(t, ctx, service, OperationFull)

	after := xgStates(t, ctx, db)
	if after["game-001"].Penalties == nil || *after["game-001"].Penalties != *before["game-001"].Penalties {
		t.Fatalf("rejected game split changed: %+v", after["game-001"].Penalties)
	}
	if result.penalties.Accepted != 1 || result.penalties.Rejected != 1 {
		t.Fatalf("outcome = %+v, want 1 accepted / 1 rejected", result.penalties)
	}
	if got := after["game-002"].Penalties; got == nil || got.HomeXG != .3 || got.HomeGoals != 1 {
		t.Fatalf("other game split = %+v, want updated", got)
	}
	if got := logs.penaltyWarnings(); got != 1 {
		t.Fatalf("sync.penalty_xg WARN logs = %d, want 1", got)
	}
}

func TestPenaltyOnlyChangeUpdatesRowWithoutMaterialChange(t *testing.T) {
	ctx, fake, db, service, scenario := penaltyFixture(t)
	executeXG(t, ctx, service, OperationFull)

	scenario.WithPenalty("game-001", asatest.PenaltyPattern{HomeXG: .76, AwayXG: .3, HomeGoals: 1, HomeShots: 1, AwayShots: 1})
	fake.Load(scenario)
	result := executeXG(t, ctx, service, OperationFull)

	if result.XG == nil || result.XG.Audit.RowsUpdated != 1 || result.XGInputsChanged || result.XG.Audit.DownstreamInputsChanged {
		t.Fatalf("result audit = %+v inputsChanged=%t", result.XG, result.XGInputsChanged)
	}
	if got := xgStates(t, ctx, db)["game-001"].Penalties; got == nil || got.AwayXG != .3 {
		t.Fatalf("split = %+v, want away xG .3", got)
	}
}

func TestPenaltyWindowRequiresParseableKickoff(t *testing.T) {
	_, err := penaltyFilters(Operation{Mode: OperationTargeted, Stage: penaltyStage}, []asa.GameXGoals{{GameID: "g"}})
	if err == nil {
		t.Fatal("expected an error for a missing date_time_utc")
	}
	filters, err := penaltyFilters(Operation{Mode: OperationTargeted, Stage: penaltyStage}, []asa.GameXGoals{
		{GameID: "a", DateTimeUTC: "2025-11-02 22:15:00 UTC"}, {GameID: "b", DateTimeUTC: "2025-12-31 23:59:00 UTC"},
	})
	if err != nil || filters.StartDate != "2025-11-01" || filters.EndDate != "2026-01-01" || filters.SeasonName != "" {
		t.Fatalf("filters = %+v, %v", filters, err)
	}
}

func TestValidatePenaltyGame(t *testing.T) {
	game := asa.GameXGoals{GameID: "g", HomeTeamID: "h", AwayTeamID: "a", HomeTeamXGoals: 1.2, AwayTeamXGoals: .4}
	pair := func(mutate func(home, away *asa.TeamGamePenaltyXGoals)) []asa.TeamGamePenaltyXGoals {
		home := asa.TeamGamePenaltyXGoals{TeamID: "h", GameID: "g", ShotsFor: 1, GoalsFor: 1, XGoalsFor: .76}
		away := asa.TeamGamePenaltyXGoals{TeamID: "a", GameID: "g", ShotsAgainst: 1, GoalsAgainst: 1, XGoalsAgainst: .76}
		if mutate != nil {
			mutate(&home, &away)
		}
		return []asa.TeamGamePenaltyXGoals{away, home} // order must not matter
	}
	tests := []struct {
		name   string
		rows   []asa.TeamGamePenaltyXGoals
		accept bool
	}{
		{name: "consistent", rows: pair(nil), accept: true},
		{name: "rounding within tolerance", rows: pair(func(h, _ *asa.TeamGamePenaltyXGoals) { h.XGoalsFor = .76004 }), accept: true},
		{name: "no rows"},
		{name: "one row", rows: pair(nil)[:1]},
		{name: "three rows", rows: append(pair(nil), asa.TeamGamePenaltyXGoals{TeamID: "h", GameID: "g"})},
		{name: "wrong team", rows: pair(func(h, _ *asa.TeamGamePenaltyXGoals) { h.TeamID = "x" })},
		{name: "same team twice", rows: pair(func(_, a *asa.TeamGamePenaltyXGoals) { a.TeamID = "h" })},
		{name: "xG disagrees", rows: pair(func(h, _ *asa.TeamGamePenaltyXGoals) { h.XGoalsFor = .5 })},
		{name: "goals disagree", rows: pair(func(h, _ *asa.TeamGamePenaltyXGoals) { h.GoalsFor = 0 })},
		{name: "shots disagree", rows: pair(func(h, _ *asa.TeamGamePenaltyXGoals) { h.ShotsFor = 2 })},
		{name: "reverse side disagrees", rows: pair(func(h, _ *asa.TeamGamePenaltyXGoals) { h.XGoalsAgainst = .2 })},
		{name: "negative", rows: pair(func(h, a *asa.TeamGamePenaltyXGoals) { h.XGoalsFor, a.XGoalsAgainst = -.1, -.1 })},
		{name: "not finite", rows: pair(func(h, a *asa.TeamGamePenaltyXGoals) { h.XGoalsFor, a.XGoalsAgainst = math.NaN(), math.NaN() })},
		{name: "negative shots", rows: pair(func(h, a *asa.TeamGamePenaltyXGoals) { h.ShotsFor, a.ShotsAgainst = -1, -1 })},
		{name: "exceeds game xG", rows: pair(func(h, a *asa.TeamGamePenaltyXGoals) { h.XGoalsFor, a.XGoalsAgainst = 1.3, 1.3 })},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			split, raw, err := validatePenaltyGame(game, test.rows)
			if test.accept != (err == nil) {
				t.Fatalf("err = %v, accept = %t", err, test.accept)
			}
			if test.accept && (split == nil || split.HomeGoals != 1 || split.HomeShots != 1 || split.AwayShots != 0 || raw == "" || raw[0] != '[') {
				t.Fatalf("split = %+v raw = %q", split, raw)
			}
		})
	}
}
