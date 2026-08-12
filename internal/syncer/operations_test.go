package syncer

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/asa"
	"github.com/jrduncans/nwsl-season/internal/cache"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestExecuteTargetedOperationsUseOneSortedRequestAndPreserveOmissions(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	seedOperationScope(t, ctx, db, "one", "two")
	dueOne := time.Date(2032, 1, 2, 2, 0, 0, 0, time.UTC)
	dueTwo := time.Date(2032, 1, 2, 3, 0, 0, 0, time.UTC)
	client := &operationASA{games: map[string][]asa.Game{"one,two": {testGame("one", "FullTime", ptr(1), ptr(0))}}, xg: map[string][]asa.GameXGoals{"one,two": {}}}
	service := Service{ASA: client, Store: db, Now: fixedOperationClock(time.Date(2032, 1, 1, 1, 0, 0, 0, time.UTC))}

	gameResult, err := service.Execute(ctx, Operation{Resource: OperationGames, Mode: OperationTargeted, Season: "2024", Stage: "Regular Season", Trigger: cache.SourceTriggerScheduler, Requested: []OperationGameRequest{{GameID: "two", NextDueAt: &dueTwo}, {GameID: "one", NextDueAt: &dueOne}}})
	if err != nil {
		t.Fatal(err)
	}
	if gameResult.Games == nil || gameResult.Games.Audit.RequestedRows != 2 || gameResult.Games.Audit.ReturnedRows != 1 || !reflect.DeepEqual(client.calls, []string{"games:one,two"}) {
		t.Fatalf("targeted games result/calls = %+v %v", gameResult.Games, client.calls)
	}
	for id, due := range map[string]time.Time{"one": dueOne, "two": dueTwo} {
		state, ok, stateErr := db.GameResultCheckState(ctx, id)
		if stateErr != nil || !ok || state.NextDueAt == nil || !state.NextDueAt.Equal(due) {
			t.Fatalf("game %s check state = %+v, ok=%t, err=%v", id, state, ok, stateErr)
		}
	}

	client.calls = nil
	xgResult, err := service.Execute(ctx, Operation{Resource: OperationGameXG, Mode: OperationTargeted, Season: "2024", Stage: "Regular Season", Trigger: cache.SourceTriggerScheduler, Requested: []OperationGameRequest{{GameID: "two", NextDueAt: &dueTwo}, {GameID: "one", NextDueAt: &dueOne}}})
	if err != nil {
		t.Fatal(err)
	}
	if xgResult.XG == nil || xgResult.XG.Audit.RequestedRows != 2 || xgResult.XG.Audit.ReturnedRows != 0 || !reflect.DeepEqual(client.calls, []string{"xg:one,two"}) {
		t.Fatalf("targeted xG result/calls = %+v %v", xgResult.XG, client.calls)
	}
	values, err := db.GameXGStates(ctx, "2024", "Regular Season")
	if err != nil || len(values) != 0 {
		t.Fatalf("targeted omission fabricated xG values = %+v, err=%v", values, err)
	}
	for id, due := range map[string]time.Time{"one": dueOne, "two": dueTwo} {
		state, ok, stateErr := db.GameXGCheckState(ctx, id)
		if stateErr != nil || !ok || state.NextDueAt == nil || !state.NextDueAt.Equal(due) {
			t.Fatalf("xG %s check state = %+v, ok=%t, err=%v", id, state, ok, stateErr)
		}
	}
}

func TestExecuteRecoversUnknownTeamsWithoutRefetchingGames(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	client := &operationASA{teams: testTeams(), games: map[string][]asa.Game{"": {testGame("one", "FullTime", ptr(1), ptr(0))}}}
	clock := fixedOperationClock(time.Date(2032, 2, 1, 0, 0, 0, 0, time.UTC), time.Date(2032, 2, 1, 0, 1, 0, 0, time.UTC))
	result, err := (Service{ASA: client, Store: db, Now: clock}).Execute(ctx, Operation{Resource: OperationGames, Mode: OperationFull, Season: "2024", Stage: "Regular Season", Trigger: cache.SourceTriggerScheduler})
	if err != nil {
		t.Fatal(err)
	}
	if result.Games == nil || result.TeamAudit == nil || !reflect.DeepEqual(client.calls, []string{"games:", "teams:"}) {
		t.Fatalf("unknown-team recovery = result %+v calls %v", result, client.calls)
	}
	if result.Games.Audit.FinishedAt.IsZero() || result.TeamAudit.FinishedAt.IsZero() || !result.Operation.FinishedAt.Equal(result.Games.Audit.FinishedAt) {
		t.Fatalf("operations did not capture finished observations: %+v %+v", result.Games.Audit, result.TeamAudit)
	}
}

func TestExecuteRecordsCachedAndASAGameFreshness(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	seedOperationScope(t, ctx, db, "one")
	incoming := testGame("one", "FullTime", ptr(1), ptr(0))
	incoming.HomeScore = ptr(2)
	incoming.LastUpdatedUTC = "2024-11-07 16:57:43 UTC"
	client := &operationASA{games: map[string][]asa.Game{"one": {incoming}}}

	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		_ = provider.Shutdown(context.Background())
	})

	service := Service{ASA: client, Store: db, Now: fixedOperationClock(time.Date(2032, 1, 1, 1, 0, 0, 0, time.UTC))}
	if _, err := service.Execute(ctx, Operation{Resource: OperationGames, Mode: OperationTargeted, Season: "2024", Stage: "Regular Season", Trigger: cache.SourceTriggerScheduler, Requested: []OperationGameRequest{{GameID: "one"}}}); err != nil {
		t.Fatal(err)
	}

	for _, span := range exporter.GetSpans() {
		if span.Name != "sync.source_operation" {
			continue
		}
		for _, event := range span.Events {
			if event.Name != "sync.game_freshness" {
				continue
			}
			values := map[string]string{}
			valueChanged := false
			intValues := map[string]int64{}
			for _, value := range event.Attributes {
				if value.Key == "nwsl.sync.source_value_changed" {
					valueChanged = value.Value.AsBool()
					continue
				}
				switch string(value.Key) {
				case "nwsl.sync.old.home_score", "nwsl.sync.new.home_score":
					intValues[string(value.Key)] = value.Value.AsInt64()
					continue
				}
				values[string(value.Key)] = value.Value.AsString()
			}
			if values["nwsl.cache.game.last_updated_utc"] != "2024-11-07 15:57:43 UTC" || values["nwsl.asa.game.last_updated_utc"] != incoming.LastUpdatedUTC || values["nwsl.sync.decision"] != "updated" || values["nwsl.sync.reason"] != "asa_last_updated_newer" || values["nwsl.sync.update_kind"] != "value_changed" || values["nwsl.sync.old.status"] != "FullTime" || values["nwsl.sync.new.status"] != "FullTime" || !valueChanged || intValues["nwsl.sync.old.home_score"] != 1 || intValues["nwsl.sync.new.home_score"] != 2 {
				t.Fatalf("freshness event = %#v ints=%#v value_changed=%t", values, intValues, valueChanged)
			}
			return
		}
	}
	t.Fatal("sync.game_freshness event not recorded")
}

func TestExecuteRecordsRejectedStaleGameResponseTelemetry(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	seedOperationScope(t, ctx, db, "one")
	incoming := testGame("one", "FullTime", ptr(2), ptr(0))
	incoming.LastUpdatedUTC = "2024-11-07 14:57:43 UTC"
	client := &operationASA{games: map[string][]asa.Game{"one": {incoming}}}

	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		_ = provider.Shutdown(context.Background())
	})

	service := Service{ASA: client, Store: db, Now: fixedOperationClock(time.Date(2032, 1, 1, 1, 0, 0, 0, time.UTC))}
	result, err := service.Execute(ctx, Operation{Resource: OperationGames, Mode: OperationTargeted, Season: "2024", Stage: "Regular Season", Trigger: cache.SourceTriggerScheduler, Requested: []OperationGameRequest{{GameID: "one"}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.GameFreshness.ResponseRejected != 1 || result.GameFreshness.StaleRejected != 1 || result.GameFreshness.ValueChanged != 0 {
		t.Fatalf("stale game freshness = %+v", result.GameFreshness)
	}

	for _, span := range exporter.GetSpans() {
		if span.Name != "sync.source_operation" {
			continue
		}
		aggregate := map[string]int64{}
		aggregateBool := map[string]bool{}
		for _, value := range span.Attributes {
			switch string(value.Key) {
			case "nwsl.sync.source_response_rejected_count", "nwsl.sync.source_stale_response_count":
				aggregate[string(value.Key)] = value.Value.AsInt64()
			case "nwsl.sync.source_response_rejected":
				aggregateBool[string(value.Key)] = value.Value.AsBool()
			}
		}
		if !aggregateBool["nwsl.sync.source_response_rejected"] || aggregate["nwsl.sync.source_response_rejected_count"] != 1 || aggregate["nwsl.sync.source_stale_response_count"] != 1 {
			t.Fatalf("stale game aggregate = %#v / %#v", aggregateBool, aggregate)
		}
		for _, event := range span.Events {
			if event.Name != "sync.game_freshness" {
				continue
			}
			values := map[string]string{}
			booleans := map[string]bool{}
			for _, value := range event.Attributes {
				switch string(value.Key) {
				case "nwsl.sync.decision", "nwsl.sync.reason", "nwsl.sync.rejection_kind", "nwsl.sync.rejection_reason":
					values[string(value.Key)] = value.Value.AsString()
				case "nwsl.sync.response_accepted", "nwsl.sync.response_rejected", "nwsl.sync.source_value_changed":
					booleans[string(value.Key)] = value.Value.AsBool()
				}
			}
			if values["nwsl.sync.decision"] != "not_updated" || values["nwsl.sync.reason"] != "asa_last_updated_not_newer" || values["nwsl.sync.rejection_kind"] != "stale" || values["nwsl.sync.rejection_reason"] != "asa_last_updated_not_newer" || booleans["nwsl.sync.response_accepted"] || !booleans["nwsl.sync.response_rejected"] || booleans["nwsl.sync.source_value_changed"] {
				t.Fatalf("stale game event = %#v / %#v", values, booleans)
			}
			return
		}
		t.Fatal("sync.game_freshness event not recorded")
	}
	t.Fatal("source operation span not recorded")
}

func TestExecuteRecordsActualXGValueCorrectionTelemetry(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	seedOperationScope(t, ctx, db, "one")
	initial := cache.GameXG{GameID: "one", Availability: cache.XGAvailable, HomeTeamID: "home", AwayTeamID: "away", HomeXG: sql.NullFloat64{Float64: 1.1, Valid: true}, AwayXG: sql.NullFloat64{Float64: 0.8, Valid: true}, RawJSON: `{}`}
	if _, err := db.ReplaceStageXG(ctx, "2024", "Regular Season", []cache.GameXG{initial}, cache.FullRefreshMetadata{Trigger: cache.SourceTriggerBackfill, StartedAt: time.Date(2031, 12, 1, 0, 0, 0, 0, time.UTC), FinishedAt: time.Date(2031, 12, 1, 0, 1, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
	client := &operationASA{xg: map[string][]asa.GameXGoals{"one": {{GameID: "one", HomeTeamID: "home", AwayTeamID: "away", HomeTeamXGoals: 2.2, AwayTeamXGoals: .8}}}}
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		_ = provider.Shutdown(context.Background())
	})

	service := Service{ASA: client, Store: db, Now: fixedOperationClock(time.Date(2032, 1, 1, 1, 0, 0, 0, time.UTC))}
	if _, err := service.Execute(ctx, Operation{Resource: OperationGameXG, Mode: OperationTargeted, Season: "2024", Stage: "Regular Season", Trigger: cache.SourceTriggerScheduler, Requested: []OperationGameRequest{{GameID: "one"}}}); err != nil {
		t.Fatal(err)
	}
	for _, span := range exporter.GetSpans() {
		if span.Name != "sync.source_operation" {
			continue
		}
		attributes := map[string]bool{}
		counts := map[string]int{}
		for _, value := range span.Attributes {
			switch string(value.Key) {
			case "nwsl.sync.source_value_changed":
				attributes[string(value.Key)] = value.Value.AsBool()
			case "nwsl.sync.source_value_changed_count":
				counts[string(value.Key)] = int(value.Value.AsInt64())
			}
		}
		if !attributes["nwsl.sync.source_value_changed"] || counts["nwsl.sync.source_value_changed_count"] != 1 {
			t.Fatalf("xG correction span attributes = %#v / %#v", attributes, counts)
		}
		for _, event := range span.Events {
			if event.Name != "sync.xg_freshness" {
				continue
			}
			values := map[string]string{}
			valueChanged := false
			floatValues := map[string]float64{}
			for _, value := range event.Attributes {
				if value.Key == "nwsl.sync.source_value_changed" {
					valueChanged = value.Value.AsBool()
					continue
				}
				switch string(value.Key) {
				case "nwsl.sync.old.home_xg", "nwsl.sync.new.home_xg":
					floatValues[string(value.Key)] = value.Value.AsFloat64()
					continue
				}
				values[string(value.Key)] = value.Value.AsString()
			}
			if values["nwsl.sync.update_kind"] != "value_changed" || !valueChanged || floatValues["nwsl.sync.old.home_xg"] != 1.1 || floatValues["nwsl.sync.new.home_xg"] != 2.2 {
				t.Fatalf("xG freshness event = %#v floats=%#v value_changed=%t", values, floatValues, valueChanged)
			}
			return
		}
		t.Fatal("xG freshness event not recorded")
	}
	t.Fatal("source operation span not recorded")
}

func TestExecuteRecordsRejectedStaleXGResponseTelemetry(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	seedOperationScope(t, ctx, db, "one")
	initial := cache.GameXG{GameID: "one", Availability: cache.XGAvailable, HomeTeamID: "home", AwayTeamID: "away", HomeXG: sql.NullFloat64{Float64: 1.1, Valid: true}, AwayXG: sql.NullFloat64{Float64: 0.8, Valid: true}, RawJSON: `{}`}
	if _, err := db.ReplaceStageXG(ctx, "2024", "Regular Season", []cache.GameXG{initial}, cache.FullRefreshMetadata{Trigger: cache.SourceTriggerBackfill, StartedAt: time.Date(2033, 12, 1, 0, 0, 0, 0, time.UTC), FinishedAt: time.Date(2033, 12, 1, 0, 1, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
	client := &operationASA{xg: map[string][]asa.GameXGoals{"one": {{GameID: "one", HomeTeamID: "home", AwayTeamID: "away", HomeTeamXGoals: 2.2, AwayTeamXGoals: .8}}}}
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		_ = provider.Shutdown(context.Background())
	})

	service := Service{ASA: client, Store: db, Now: fixedOperationClock(time.Date(2032, 1, 1, 1, 0, 0, 0, time.UTC))}
	result, err := service.Execute(ctx, Operation{Resource: OperationGameXG, Mode: OperationTargeted, Season: "2024", Stage: "Regular Season", Trigger: cache.SourceTriggerScheduler, Requested: []OperationGameRequest{{GameID: "one"}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.XG == nil || len(result.XG.Freshness) != 1 || !result.XG.Freshness[0].ResponseRejected || result.XG.Freshness[0].RejectionKind != "stale" {
		t.Fatalf("stale xG freshness = %+v", result.XG)
	}

	for _, span := range exporter.GetSpans() {
		if span.Name != "sync.source_operation" {
			continue
		}
		aggregate := map[string]int64{}
		aggregateBool := map[string]bool{}
		for _, value := range span.Attributes {
			switch string(value.Key) {
			case "nwsl.sync.source_value_changed_count", "nwsl.sync.source_response_rejected_count", "nwsl.sync.source_stale_response_count":
				aggregate[string(value.Key)] = value.Value.AsInt64()
			case "nwsl.sync.source_value_changed", "nwsl.sync.source_response_rejected":
				aggregateBool[string(value.Key)] = value.Value.AsBool()
			}
		}
		if aggregateBool["nwsl.sync.source_value_changed"] || aggregate["nwsl.sync.source_value_changed_count"] != 0 || !aggregateBool["nwsl.sync.source_response_rejected"] || aggregate["nwsl.sync.source_response_rejected_count"] != 1 || aggregate["nwsl.sync.source_stale_response_count"] != 1 {
			t.Fatalf("stale xG aggregate = %#v / %#v", aggregateBool, aggregate)
		}
		for _, event := range span.Events {
			if event.Name != "sync.xg_freshness" {
				continue
			}
			values := map[string]string{}
			booleans := map[string]bool{}
			for _, value := range event.Attributes {
				switch string(value.Key) {
				case "nwsl.sync.rejection_kind", "nwsl.sync.rejection_reason":
					values[string(value.Key)] = value.Value.AsString()
				case "nwsl.sync.response_accepted", "nwsl.sync.response_rejected", "nwsl.sync.source_value_changed":
					booleans[string(value.Key)] = value.Value.AsBool()
				}
			}
			if values["nwsl.sync.rejection_kind"] != "stale" || values["nwsl.sync.rejection_reason"] != "observation_not_newer_than_cached_check" || booleans["nwsl.sync.response_accepted"] || !booleans["nwsl.sync.response_rejected"] || booleans["nwsl.sync.source_value_changed"] {
				t.Fatalf("stale xG event = %#v / %#v", values, booleans)
			}
			return
		}
		t.Fatal("sync.xg_freshness event not recorded")
	}
	t.Fatal("source operation span not recorded")
}

func TestExecuteFullOperationOwnsExactlyOneResourceRequest(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	client := &operationASA{teams: testTeams(), games: map[string][]asa.Game{"": {testGame("one", "FullTime", ptr(1), ptr(0))}}, xg: map[string][]asa.GameXGoals{"": {{GameID: "one", HomeTeamID: "home", AwayTeamID: "away", HomeTeamXGoals: 1.2, AwayTeamXGoals: .8}}}}
	service := Service{ASA: client, Store: db, Now: fixedOperationClock(time.Date(2032, 4, 1, 0, 0, 0, 0, time.UTC))}

	if _, err := service.Execute(ctx, Operation{Resource: OperationTeams, Mode: OperationFull, Trigger: cache.SourceTriggerCLI}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(client.calls, []string{"teams:"}) {
		t.Fatalf("team operation calls = %v", client.calls)
	}
	client.calls = nil
	if _, err := service.Execute(ctx, Operation{Resource: OperationGames, Mode: OperationFull, Season: "2024", Stage: "Regular Season", Trigger: cache.SourceTriggerCLI}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(client.calls, []string{"games:"}) {
		t.Fatalf("full games operation calls = %v", client.calls)
	}
	client.calls = nil
	if _, err := service.Execute(ctx, Operation{Resource: OperationGameXG, Mode: OperationFull, Season: "2024", Stage: "Regular Season", Trigger: cache.SourceTriggerCLI}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(client.calls, []string{"xg:"}) {
		t.Fatalf("full xG operation calls = %v", client.calls)
	}
}

func TestExecuteRecordsOneGeneralizedFailureForOwningOperation(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	client := &operationASA{gamesErr: context.DeadlineExceeded}
	_, err := (Service{ASA: client, Store: db, Now: fixedOperationClock(time.Date(2032, 5, 1, 0, 0, 0, 0, time.UTC))}).Execute(ctx, Operation{Resource: OperationGames, Mode: OperationFull, Season: "2024", Stage: "Regular Season", Trigger: cache.SourceTriggerScheduler})
	if err == nil {
		t.Fatal("Execute() error = nil, want fetch failure")
	}
	audits, auditErr := db.SourceRefreshAudits(ctx, cache.SourceResourceGames, "2024", "Regular Season")
	if auditErr != nil || len(audits) != 1 || audits[0].Outcome != cache.SourceRefreshFailure || audits[0].Mode != cache.SourceRefreshFull {
		t.Fatalf("failure audits = %+v, err=%v", audits, auditErr)
	}
}

func TestRunCompatibilityUsesSplitOperationsAndSkipsXGForEmptyDiscovery(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	client := &operationASA{teams: testTeams(), games: map[string][]asa.Game{"": {}}}
	run, err := (Service{ASA: client, Store: db, Now: fixedOperationClock(time.Date(2032, 3, 1, 0, 0, 0, 0, time.UTC))}).Run(ctx, RunOptions{Season: "2024", Stage: "Regular Season", Trigger: "cli"})
	if err != nil {
		t.Fatal(err)
	}
	if run.ID != 0 || !reflect.DeepEqual(client.calls, []string{"games:"}) {
		t.Fatalf("empty compatibility discovery run/calls = %+v %v", run, client.calls)
	}
	audits, err := db.SourceRefreshAudits(ctx, cache.SourceResourceGames, "2024", "Regular Season")
	if err != nil || len(audits) != 1 || audits[0].Outcome != cache.SourceRefreshSuccess {
		t.Fatalf("empty game discovery audit = %+v, err=%v", audits, err)
	}
}

func seedOperationScope(t *testing.T, ctx context.Context, db *cache.DB, ids ...string) {
	t.Helper()
	now := time.Date(2031, 12, 1, 0, 0, 0, 0, time.UTC)
	if _, err := db.UpsertTeams(ctx, []cache.Team{{ASAID: "home", Name: "Home"}, {ASAID: "away", Name: "Away"}}, cache.FullRefreshMetadata{Trigger: cache.SourceTriggerBackfill, StartedAt: now, FinishedAt: now}); err != nil {
		t.Fatal(err)
	}
	games := make([]cache.Game, 0, len(ids))
	for _, id := range ids {
		games = append(games, cache.Game{ASAID: id, Season: "2024", Stage: "Regular Season", KickoffUTC: "2024-11-03 22:30:00 UTC", Status: "FullTime", HomeTeamID: "home", AwayTeamID: "away", HomeScore: cacheScore(1), AwayScore: cacheScore(0), LastUpdatedUTC: "2024-11-07 15:57:43 UTC", RawJSON: `{}`})
	}
	if _, err := db.ReplaceGameInventory(ctx, "2024", "Regular Season", games, nil, cache.FullRefreshMetadata{Trigger: cache.SourceTriggerBackfill, StartedAt: now, FinishedAt: now}); err != nil {
		t.Fatal(err)
	}
}

func cacheScore(value int) (result sql.NullInt64) {
	return sql.NullInt64{Int64: int64(value), Valid: true}
}

type operationASA struct {
	teams    []asa.Team
	games    map[string][]asa.Game
	xg       map[string][]asa.GameXGoals
	gamesErr error
	calls    []string
}

func (f *operationASA) Teams(context.Context, asa.TeamsFilters) ([]asa.Team, error) {
	f.calls = append(f.calls, "teams:")
	return append([]asa.Team(nil), f.teams...), nil
}
func (f *operationASA) Games(_ context.Context, filters asa.GamesFilters) ([]asa.Game, error) {
	f.calls = append(f.calls, "games:"+filters.GameID)
	if f.gamesErr != nil {
		return nil, f.gamesErr
	}
	return append([]asa.Game(nil), f.games[filters.GameID]...), nil
}
func (f *operationASA) GameXGoals(_ context.Context, filters asa.XGoalsFilters) ([]asa.GameXGoals, error) {
	f.calls = append(f.calls, "xg:"+filters.GameID)
	return append([]asa.GameXGoals(nil), f.xg[filters.GameID]...), nil
}

func fixedOperationClock(values ...time.Time) func() time.Time {
	index := 0
	return func() time.Time {
		if index < len(values)-1 {
			value := values[index]
			index++
			return value
		}
		return values[len(values)-1]
	}
}
