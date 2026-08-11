package syncer

import (
	"context"
	"errors"
	"math"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/asa"
	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/global"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestRunIsIdempotentAndUpdatesGames(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	client := fakeASA{
		teams: testTeams(),
		games: []asa.Game{
			testGame("game-1", "FullTime", ptr(1), ptr(0)),
			testGame("game-2", "PreMatch", nil, nil),
		},
	}
	service := Service{ASA: &client, Store: db}

	first, err := service.Run(ctx, RunOptions{Season: "2024", Stage: "Regular Season"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := client.gamesFilters.Status, "Abandoned,FullTime,PreMatch"; got != want {
		t.Fatalf("game status filter = %q, want %q", got, want)
	}
	if first.GamesUpserted != 2 || first.GamesDeleted != 0 {
		t.Fatalf("first run counts = %+v, want 2 upserted and 0 deleted", first)
	}
	if first.TeamsUpserted != 2 || first.TeamsInserted != 2 {
		t.Fatalf("first run team counts = %+v, want recovered catalog counts", first)
	}

	client.games[0] = testGame("game-1", "FullTime", ptr(2), ptr(2))
	client.games[0].LastUpdatedUTC = "2024-11-07 16:57:43 UTC"
	second, err := service.Run(ctx, RunOptions{Season: "2024", Stage: "Regular Season"})
	if err != nil {
		t.Fatal(err)
	}
	if second.GamesUpserted != 2 || second.GamesDeleted != 0 {
		t.Fatalf("second run counts = %+v, want 2 upserted and 0 deleted", second)
	}

	count := cachedGameCount(t, ctx, db, "2024", "Regular Season")
	if count != 2 {
		t.Fatalf("cached game count = %d, want 2", count)
	}

	game := cachedGame(t, ctx, db, "2024", "Regular Season", "game-1")
	if !game.HomeScore.Valid || game.HomeScore.Int64 != 2 {
		t.Fatalf("home score = %+v, want 2", game.HomeScore)
	}
	if !game.AwayScore.Valid || game.AwayScore.Int64 != 2 {
		t.Fatalf("away score = %+v, want 2", game.AwayScore)
	}
}

func TestRunRecordsDetailedTelemetry(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		_ = provider.Shutdown(context.Background())
	})

	service := Service{ASA: &fakeASA{
		teams: testTeams(),
		games: []asa.Game{testGame("game-1", "FullTime", ptr(1), ptr(0))},
	}, Store: newTestDB(t)}
	if _, err := service.Run(context.Background(), RunOptions{
		Season: "2024", Stage: "Regular Season", ExpectedTeams: 2, GamesPerTeam: 1, Trigger: "cli",
	}); err != nil {
		t.Fatal(err)
	}

	span := findTelemetrySpan(t, exporter.GetSpans(), "sync.run")
	attributes := telemetryAttributes(span)
	if got := attributes["nwsl.sync.trigger"].AsString(); got != "cli" {
		t.Errorf("nwsl.sync.trigger = %q, want cli", got)
	}
	if got := attributes["nwsl.sync.games_seen"].AsInt64(); got != 1 {
		t.Errorf("nwsl.sync.games_seen = %d, want 1", got)
	}
	if got := attributes["nwsl.sync.expected_fixture_count"].AsInt64(); got != 1 {
		t.Errorf("nwsl.sync.expected_fixture_count = %d, want 1", got)
	}
	if got := attributes["nwsl.sync.partial_failure"].AsBool(); got {
		t.Error("nwsl.sync.partial_failure = true, want false")
	}
	if got := attributes["nwsl.cache.fixture_snapshot_id"].AsString(); got == "" {
		t.Error("nwsl.cache.fixture_snapshot_id is empty")
	}
}

func TestRunRecordsEachFailureAtItsOwningBoundary(t *testing.T) {
	traceExporter := tracetest.NewInMemoryExporter()
	traceProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(traceExporter))
	previousTraceProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(traceProvider)
	logExporter := &syncerLogExporter{}
	logProvider := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(logExporter)))
	previousLogProvider := global.GetLoggerProvider()
	global.SetLoggerProvider(logProvider)
	t.Cleanup(func() {
		global.SetLoggerProvider(previousLogProvider)
		_ = logProvider.Shutdown(context.Background())
		otel.SetTracerProvider(previousTraceProvider)
		_ = traceProvider.Shutdown(context.Background())
	})

	service := Service{ASA: &fakeASA{
		teamsErr: errors.New("ASA teams unavailable"),
		games:    []asa.Game{testGame("game-1", "FullTime", ptr(1), ptr(0))},
	}, Store: newTestDB(t)}
	if _, err := service.Run(context.Background(), RunOptions{Season: "2024", Stage: "Regular Season"}); err == nil {
		t.Fatal("Run() error = nil, want fetch failure")
	}

	if len(logExporter.records) != 1 {
		t.Fatalf("exception log records = %d, want 1", len(logExporter.records))
	}
	if got := syncerLogAttribute(logExporter.records[0], "nwsl.error.code"); got != "sync.run" {
		t.Errorf("exception code = %q, want sync.run", got)
	}
	if got := syncerLogAttribute(logExporter.records[0], "exception.type"); got != telemetry.ErrorTypeUpstreamFailure {
		t.Errorf("fetch exception type = %q, want %q", got, telemetry.ErrorTypeUpstreamFailure)
	}
	if got := logExporter.records[0].Severity(); got != otellog.SeverityError {
		t.Errorf("fetch exception severity = %v, want ERROR", got)
	}
	span := findTelemetrySpan(t, traceExporter.GetSpans(), "sync.run")
	if span.Status.Code != codes.Error {
		t.Error("sync.run status is not error")
	}
	if got := telemetryAttributes(span)["error.type"].AsString(); got != telemetry.ErrorTypeUpstreamFailure {
		t.Errorf("sync.run error.type = %q, want %q", got, telemetry.ErrorTypeUpstreamFailure)
	}

	if _, err := (Service{}).Run(context.Background(), RunOptions{Season: "2024", Stage: "Regular Season"}); err == nil {
		t.Fatal("Run() error = nil, want configuration failure")
	}
	if len(logExporter.records) != 2 {
		t.Fatalf("exception log records after two failed runs = %d, want 2", len(logExporter.records))
	}
	if got := syncerLogAttribute(logExporter.records[1], "nwsl.error.code"); got != "sync.run" {
		t.Errorf("direct failure exception code = %q, want sync.run", got)
	}
	if got := syncerLogAttribute(logExporter.records[1], "exception.type"); got != telemetry.ErrorTypeInvalidArgument {
		t.Errorf("direct failure exception type = %q, want %q", got, telemetry.ErrorTypeInvalidArgument)
	}

	conflictOptions := RunOptions{Season: "2024", Stage: "Regular Season"}
	conflictStore := newTestDB(t)
	acquired, err := conflictStore.TryAcquireSyncLease(context.Background(), leaseKey(conflictOptions), "other-process", time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !acquired {
		t.Fatal("test lease was not acquired")
	}
	if _, err := (Service{ASA: &fakeASA{}, Store: conflictStore}).Run(context.Background(), conflictOptions); !errors.Is(err, cache.ErrSyncInProgress) {
		t.Fatalf("Run() error = %v, want ErrSyncInProgress", err)
	}
	if len(logExporter.records) != 2 {
		t.Fatalf("expected sync conflict emitted an exception; log records = %d, want 2", len(logExporter.records))
	}
	spans := traceExporter.GetSpans()
	conflictSpan := spans[len(spans)-1]
	if conflictSpan.Name != "sync.run" {
		t.Fatalf("last span = %q, want sync.run", conflictSpan.Name)
	}
	if conflictSpan.Status.Code != codes.Unset {
		t.Errorf("conflict span status = %v, want unset", conflictSpan.Status.Code)
	}
	conflictAttributes := telemetryAttributes(conflictSpan)
	if !conflictAttributes["nwsl.error.expected"].AsBool() {
		t.Error("conflict span error.expected = false, want true")
	}
	if got := conflictAttributes["nwsl.sync.outcome"].AsString(); got != "conflict" {
		t.Errorf("conflict span sync.outcome = %q, want conflict", got)
	}
}

func findTelemetrySpan(t *testing.T, spans []tracetest.SpanStub, name string) tracetest.SpanStub {
	t.Helper()
	for _, span := range spans {
		if span.Name == name {
			return span
		}
	}
	t.Fatalf("span %q was not recorded", name)
	return tracetest.SpanStub{}
}

func telemetryAttributes(span tracetest.SpanStub) map[string]attribute.Value {
	values := make(map[string]attribute.Value, len(span.Attributes))
	for _, value := range span.Attributes {
		values[string(value.Key)] = value.Value
	}
	return values
}

type syncerLogExporter struct {
	records []sdklog.Record
}

func (e *syncerLogExporter) Export(_ context.Context, records []sdklog.Record) error {
	e.records = append(e.records, records...)
	return nil
}

func (*syncerLogExporter) Shutdown(context.Context) error   { return nil }
func (*syncerLogExporter) ForceFlush(context.Context) error { return nil }

func syncerLogAttribute(record sdklog.Record, key string) string {
	value := ""
	record.WalkAttributes(func(attribute attribute.KeyValue) bool {
		if string(attribute.Key) == key {
			value = attribute.Value.AsString()
		}
		return true
	})
	return value
}

func TestEnsureVenueHistorySyncsMissingSeasonsOnce(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	client := &historyASA{calls: map[string]int{}}
	order := []string{}
	service := Service{ASA: client, Store: db, Qualification: orderedRefresher{order: &order, label: "qualification"}}
	if err := service.EnsureVenueHistory(ctx, "2026", "Regular Season", 2, time.Second); err != nil {
		t.Fatal(err)
	}
	if client.calls["2025"] != 1 || client.calls["2024"] != 1 {
		t.Fatalf("historical calls = %v, want one per season", client.calls)
	}
	if len(order) != 0 {
		t.Fatalf("historical sync ran derived calculations: %v", order)
	}
	if err := service.EnsureVenueHistory(ctx, "2026", "Regular Season", 2, time.Second); err != nil {
		t.Fatal(err)
	}
	if client.calls["2025"] != 1 || client.calls["2024"] != 1 {
		t.Fatalf("ready historical seasons were fetched again: %v", client.calls)
	}
}

func TestRunAutomaticallyPrunesHistoryWhenConfigured(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	client := fakeASA{
		teams: testTeams(),
		games: []asa.Game{testGame("game-1", "FullTime", ptr(1), ptr(0))},
	}
	service := Service{ASA: &client, Store: db, HistoryRetention: 90 * 24 * time.Hour}

	run, err := service.Run(ctx, RunOptions{Season: "2024", Stage: "Regular Season"})
	if err != nil {
		t.Fatal(err)
	}
	if run.HistoryPrune == nil {
		t.Fatal("automatic history prune was not run")
	}
	if run.HistoryPruneError != "" {
		t.Fatalf("automatic history prune error = %q", run.HistoryPruneError)
	}
}

func TestRunRefreshesXGBeforeDerivedCalculations(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	order := []string{}
	client := traceASA{fakeASA: fakeASA{
		teams: testTeams(),
		games: []asa.Game{testGame("game-1", "FullTime", ptr(1), ptr(0))},
	}, order: &order, xg: []asa.GameXGoals{{GameID: "game-1", HomeTeamID: "home", AwayTeamID: "away", HomeTeamXGoals: 1.2, AwayTeamXGoals: 0.6}}}
	service := Service{
		ASA:           &client,
		Store:         db,
		Qualification: orderedRefresher{order: &order, label: "qualification"},
		Scenarios:     orderedRefresher{order: &order, label: "scenarios"},
	}
	if _, err := service.Run(ctx, RunOptions{Season: "2024", Stage: "Regular Season"}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"xg", "qualification", "scenarios"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("refresh order = %v, want %v", order, want)
	}
}

func TestMapXGoalsPreservesExpectedPoints(t *testing.T) {
	homePoints, awayPoints := 2.47, .367
	values, err := mapXGoals([]asa.GameXGoals{{GameID: "game-1", HomeTeamID: "home", AwayTeamID: "away", HomeTeamXGoals: 2.36, AwayTeamXGoals: 1.11, HomeXPoints: &homePoints, AwayXPoints: &awayPoints}})
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || !values[0].HomeXPoints.Valid || !values[0].AwayXPoints.Valid {
		t.Fatalf("mapped values = %+v, want expected points", values)
	}
	if values[0].HomeXPoints.Float64 != homePoints || values[0].AwayXPoints.Float64 != awayPoints {
		t.Fatalf("expected points = %+v, want %.3f / %.3f", values[0], homePoints, awayPoints)
	}
}

func TestMapXGoalsValidatesExpectedPointsRange(t *testing.T) {
	floatPtr := func(value float64) *float64 { return &value }
	game := func(home, away float64) asa.GameXGoals {
		return asa.GameXGoals{GameID: "game-1", HomeTeamID: "home", AwayTeamID: "away", HomeTeamXGoals: 1.2, AwayTeamXGoals: 0.6, HomeXPoints: floatPtr(home), AwayXPoints: floatPtr(away)}
	}

	for _, points := range []struct {
		name       string
		home, away float64
	}{
		{name: "negative", home: -0.01, away: 1},
		{name: "above three", home: 3.01, away: 0},
		{name: "not a number", home: math.NaN(), away: 1},
		{name: "positive infinity", home: math.Inf(1), away: 1},
		{name: "negative infinity", home: math.Inf(-1), away: 1},
	} {
		t.Run(points.name, func(t *testing.T) {
			if _, err := mapXGoals([]asa.GameXGoals{game(points.home, points.away)}); err == nil {
				t.Fatal("mapXGoals succeeded with invalid expected points")
			}
		})
	}

	for _, points := range []struct {
		name       string
		home, away float64
	}{
		{name: "zero", home: 0, away: 0},
		{name: "three", home: 3, away: 3},
	} {
		t.Run(points.name, func(t *testing.T) {
			if _, err := mapXGoals([]asa.GameXGoals{game(points.home, points.away)}); err != nil {
				t.Fatalf("mapXGoals rejected boundary expected points: %v", err)
			}
		})
	}
}

func TestRunInvalidExpectedPointsPreservesLastGoodXGSnapshot(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	homePoints, awayPoints := 2.47, .367
	order := []string{}
	client := traceASA{fakeASA: fakeASA{
		teams: testTeams(),
		games: []asa.Game{testGame("game-1", "FullTime", ptr(1), ptr(0))},
	}, order: &order, xg: []asa.GameXGoals{{GameID: "game-1", HomeTeamID: "home", AwayTeamID: "away", HomeTeamXGoals: 1.2, AwayTeamXGoals: 0.6, HomeXPoints: &homePoints, AwayXPoints: &awayPoints}}}
	service := Service{ASA: &client, Store: db}
	if _, err := service.Run(ctx, RunOptions{Season: "2024", Stage: "Regular Season"}); err != nil {
		t.Fatal(err)
	}

	invalidPoints := 3.01
	client.xg[0].HomeXPoints = &invalidPoints
	run, err := service.Run(ctx, RunOptions{Season: "2024", Stage: "Regular Season", Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if run.XGError == "" {
		t.Fatal("xG refresh error is empty after invalid expected points")
	}

	season, err := db.Season(ctx, "2024", "Regular Season")
	if err != nil {
		t.Fatal(err)
	}
	if len(season.XGoals) != 1 || season.XGoals[0].HomeXPoints.Float64 != homePoints || season.XGoals[0].AwayXPoints.Float64 != awayPoints {
		t.Fatalf("xG values = %+v, want prior good expected-points snapshot", season.XGoals)
	}
	audits, auditErr := db.SourceRefreshAudits(ctx, cache.SourceResourceGameXG, "2024", "Regular Season")
	if auditErr != nil || len(audits) < 2 || audits[0].Outcome != cache.SourceRefreshFailure {
		t.Fatalf("xG source audits = %+v, err=%v, want generalized failure after success", audits, auditErr)
	}
}

func TestRecalculateUsesCachedFixturesWithoutCallingASA(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	client := fakeASA{
		teams: testTeams(),
		games: []asa.Game{testGame("game-1", "FullTime", ptr(1), ptr(0))},
	}
	if _, err := (Service{ASA: &client, Store: db}).Run(ctx, RunOptions{Season: "2024", Stage: "Regular Season"}); err != nil {
		t.Fatal(err)
	}
	lastSync, err := db.LastAttempt(ctx, "2024", "Regular Season")
	if err != nil {
		t.Fatal(err)
	}

	order := []string{}
	qualificationForced, scenariosForced := false, false
	service := Service{
		Store:         db,
		Qualification: orderedRefresher{order: &order, label: "qualification", forced: &qualificationForced},
		Scenarios:     orderedRefresher{order: &order, label: "scenarios", forced: &scenariosForced},
	}
	run, err := service.Recalculate(ctx, RecalculateOptions{Season: "2024", Stage: "Regular Season", Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"qualification", "scenarios"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("refresh order = %v, want %v", order, want)
	}
	if !run.QualificationRecalculated || !run.ScenarioRecalculated {
		t.Fatalf("recalculation flags = qualification %t scenarios %t, want both true", run.QualificationRecalculated, run.ScenarioRecalculated)
	}
	if !qualificationForced || !scenariosForced {
		t.Fatalf("force flags = qualification %t scenarios %t, want both true", qualificationForced, scenariosForced)
	}
	if client.teamsCalls != 1 || client.gamesCalls != 1 {
		t.Fatalf("ASA calls = teams %d games %d, want unchanged at 1 each", client.teamsCalls, client.gamesCalls)
	}
	after, err := db.LastAttempt(ctx, "2024", "Regular Season")
	if err != nil {
		t.Fatal(err)
	}
	if after == nil || lastSync == nil || after.ID != lastSync.ID {
		t.Fatalf("last sync changed from %+v to %+v", lastSync, after)
	}
}

func TestRunAllowsConsecutiveSyncs(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	client := fakeASA{
		teams: testTeams(),
		games: []asa.Game{testGame("game-1", "FullTime", ptr(1), ptr(0))},
	}
	service := Service{ASA: &client, Store: db}

	first, err := service.Run(ctx, RunOptions{Season: "2024", Stage: "Regular Season"})
	if err != nil {
		t.Fatal(err)
	}
	if client.teamsCalls != 1 || client.gamesCalls != 1 {
		t.Fatalf("ASA calls after first run = teams %d games %d, want 1 and 1", client.teamsCalls, client.gamesCalls)
	}

	client.games[0] = testGame("game-1", "FullTime", ptr(3), ptr(0))
	client.games[0].LastUpdatedUTC = "2024-11-07 16:57:43 UTC"
	second, err := service.Run(ctx, RunOptions{Season: "2024", Stage: "Regular Season"})
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == first.ID {
		t.Fatalf("second run ID = %d, want a new sync after %d", second.ID, first.ID)
	}
	if client.teamsCalls != 1 || client.gamesCalls != 2 {
		t.Fatalf("ASA calls after second run = teams %d games %d, want 1 and 2", client.teamsCalls, client.gamesCalls)
	}

	game := cachedGame(t, ctx, db, "2024", "Regular Season", "game-1")
	if !game.HomeScore.Valid || game.HomeScore.Int64 != 3 {
		t.Fatalf("home score = %+v, want refreshed score 3", game.HomeScore)
	}
}

func TestRunHardDeletesMissingGamesAfterSuccessfulFetch(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	client := fakeASA{
		teams: testTeams(),
		games: []asa.Game{
			testGame("game-1", "FullTime", ptr(1), ptr(0)),
			testGame("game-2", "PreMatch", nil, nil),
		},
	}
	service := Service{ASA: &client, Store: db}

	if _, err := service.Run(ctx, RunOptions{Season: "2024", Stage: "Regular Season"}); err != nil {
		t.Fatal(err)
	}

	client.games = []asa.Game{testGame("game-1", "FullTime", ptr(1), ptr(0))}
	run, err := service.Run(ctx, RunOptions{Season: "2024", Stage: "Regular Season"})
	if err != nil {
		t.Fatal(err)
	}
	if run.GamesDeleted != 1 {
		t.Fatalf("deleted games = %d, want 1", run.GamesDeleted)
	}

	count := cachedGameCount(t, ctx, db, "2024", "Regular Season")
	if count != 1 {
		t.Fatalf("cached game count = %d, want 1", count)
	}
}

func TestRunRejectsIncompleteKnownScheduleAndPreservesExistingRows(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	client := fakeASA{
		teams: testTeams(),
		games: []asa.Game{
			testGame("game-1", "FullTime", ptr(1), ptr(0)),
			testGame("game-2", "PreMatch", nil, nil),
		},
	}
	service := Service{ASA: &client, Store: db}
	options := RunOptions{Season: "2024", Stage: "Regular Season", ExpectedTeams: 2, GamesPerTeam: 2}

	if _, err := service.Run(ctx, options); err != nil {
		t.Fatal(err)
	}
	client.games = []asa.Game{testGame("game-1", "FullTime", ptr(1), ptr(0))}
	if _, err := service.Run(ctx, options); err == nil {
		t.Fatal("Run() error = nil, want incomplete schedule validation error")
	}
	if client.gamesCalls != 2 {
		t.Fatalf("season game calls = %d, want one request per full operation", client.gamesCalls)
	}

	if count := cachedGameCount(t, ctx, db, "2024", "Regular Season"); count != 2 {
		t.Fatalf("cached game count = %d, want original 2-game schedule preserved", count)
	}
	audits, err := db.SourceRefreshAudits(ctx, cache.SourceResourceGames, "2024", "Regular Season")
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) < 2 || audits[0].Outcome != cache.SourceRefreshFailure {
		t.Fatalf("source audits = %+v, want failed validation attempt", audits)
	}
}

func TestRunRejectsEmptyGamesAndPreservesExistingRows(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	client := fakeASA{
		teams: testTeams(),
		games: []asa.Game{testGame("game-1", "FullTime", ptr(1), ptr(0))},
	}
	service := Service{ASA: &client, Store: db}

	if _, err := service.Run(ctx, RunOptions{Season: "2024", Stage: "Regular Season"}); err != nil {
		t.Fatal(err)
	}

	client.games = []asa.Game{}
	if _, err := service.Run(ctx, RunOptions{Season: "2024", Stage: "Regular Season"}); err == nil {
		t.Fatal("err = nil, want validation error")
	}

	count := cachedGameCount(t, ctx, db, "2024", "Regular Season")
	if count != 1 {
		t.Fatalf("cached game count = %d, want existing row preserved", count)
	}

	audits, err := db.SourceRefreshAudits(ctx, cache.SourceResourceGames, "2024", "Regular Season")
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) < 2 || audits[0].Outcome != cache.SourceRefreshFailure {
		t.Fatalf("source audits = %+v, want failure", audits)
	}
}

func TestRunRejectsSelfFixtureAndPreservesExistingRows(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	client := fakeASA{
		teams: testTeams(),
		games: []asa.Game{testGame("game-1", "FullTime", ptr(1), ptr(0))},
	}
	service := Service{ASA: &client, Store: db}

	if _, err := service.Run(ctx, RunOptions{Season: "2024", Stage: "Regular Season"}); err != nil {
		t.Fatal(err)
	}

	selfFixture := testGame("game-2", "FullTime", ptr(2), ptr(0))
	selfFixture.AwayTeamID = selfFixture.HomeTeamID
	client.games = []asa.Game{selfFixture}
	if _, err := service.Run(ctx, RunOptions{Season: "2024", Stage: "Regular Season"}); err == nil {
		t.Fatal("err = nil, want self-fixture validation error")
	}

	if count := cachedGameCount(t, ctx, db, "2024", "Regular Season"); count != 1 {
		t.Fatalf("cached game count = %d, want previous fixture set unchanged", count)
	}
	game := cachedGame(t, ctx, db, "2024", "Regular Season", "game-1")
	if !game.HomeScore.Valid || game.HomeScore.Int64 != 1 || !game.AwayScore.Valid || game.AwayScore.Int64 != 0 {
		t.Fatalf("cached game = %+v, want previous successful fixture", game)
	}

	audits, err := db.SourceRefreshAudits(ctx, cache.SourceResourceGames, "2024", "Regular Season")
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) < 2 || audits[0].Outcome != cache.SourceRefreshFailure {
		t.Fatalf("source audits = %+v, want recorded failure", audits)
	}
}

func TestRunRecordsFetchFailureAndPreservesExistingRows(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	client := fakeASA{
		teams: testTeams(),
		games: []asa.Game{testGame("game-1", "FullTime", ptr(1), ptr(0))},
	}
	service := Service{ASA: &client, Store: db}

	if _, err := service.Run(ctx, RunOptions{Season: "2024", Stage: "Regular Season"}); err != nil {
		t.Fatal(err)
	}

	client.gamesErr = errors.New("upstream down")
	if _, err := service.Run(ctx, RunOptions{Season: "2024", Stage: "Regular Season"}); err == nil {
		t.Fatal("err = nil, want fetch error")
	}

	count := cachedGameCount(t, ctx, db, "2024", "Regular Season")
	if count != 1 {
		t.Fatalf("cached game count = %d, want existing row preserved", count)
	}
}

func TestRunAllowsSyncAfterFailedAttempt(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	client := fakeASA{teams: testTeams(), games: []asa.Game{testGame("game-1", "FullTime", ptr(1), ptr(0))}}
	service := Service{ASA: &client, Store: db}

	client.teamsErr = errors.New("upstream down")
	if _, err := service.Run(ctx, RunOptions{Season: "2024", Stage: "Regular Season"}); err == nil {
		t.Fatal("first run error = nil, want failure")
	}
	if client.teamsCalls != 1 {
		t.Fatalf("teams calls = %d, want 1", client.teamsCalls)
	}

	client.teamsErr = nil
	run, err := service.Run(ctx, RunOptions{Season: "2024", Stage: "Regular Season"})
	if err != nil {
		t.Fatal(err)
	}
	if run.Outcome != "success" || client.teamsCalls != 2 || client.gamesCalls != 2 {
		t.Fatalf("retry run = %+v; calls teams=%d games=%d, want successful second attempt", run, client.teamsCalls, client.gamesCalls)
	}
}

func cachedGameCount(t *testing.T, ctx context.Context, db *cache.DB, season, stage string) int {
	t.Helper()
	data, err := db.Season(ctx, season, stage)
	if err != nil {
		t.Fatal(err)
	}
	return len(data.Games)
}

func cachedGame(t *testing.T, ctx context.Context, db *cache.DB, season, stage, id string) cache.Game {
	t.Helper()
	data, err := db.Season(ctx, season, stage)
	if err != nil {
		t.Fatal(err)
	}
	for _, game := range data.Games {
		if game.ASAID == id {
			return game
		}
	}
	t.Fatalf("cached game %q not found", id)
	return cache.Game{}
}

func newTestDB(t *testing.T) *cache.DB {
	t.Helper()
	db, err := cache.Open(context.Background(), t.TempDir()+"/cache.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	})
	return db
}

type fakeASA struct {
	mu            sync.Mutex
	teams         []asa.Team
	teamsErr      error
	teamsCalls    int
	games         []asa.Game
	gameResponses [][]asa.Game
	gamesErr      error
	gamesCalls    int
	gamesFilters  asa.GamesFilters
	targetGames   map[string][]asa.Game
	targetErrors  map[string]error
	targetCalls   map[string]int
}

type historyASA struct{ calls map[string]int }

func (f *historyASA) Teams(context.Context, asa.TeamsFilters) ([]asa.Team, error) {
	return testTeams(), nil
}

func (f *historyASA) Games(_ context.Context, filters asa.GamesFilters) ([]asa.Game, error) {
	f.calls[filters.SeasonName]++
	game := testGame("game-"+filters.SeasonName, "FullTime", ptr(2), ptr(1))
	game.SeasonName = filters.SeasonName
	return []asa.Game{game}, nil
}

func (f *historyASA) GameXGoals(_ context.Context, filters asa.XGoalsFilters) ([]asa.GameXGoals, error) {
	return []asa.GameXGoals{{GameID: "game-" + filters.SeasonName, HomeTeamID: "home", AwayTeamID: "away", HomeTeamXGoals: 1.5, AwayTeamXGoals: .8}}, nil
}

type blockingASA struct {
	teamsStarted chan struct{}
	gamesStarted chan struct{}
	xgStarted    chan struct{}
	release      <-chan struct{}
}

func (f *blockingASA) Teams(context.Context, asa.TeamsFilters) ([]asa.Team, error) {
	close(f.teamsStarted)
	<-f.release
	return testTeams(), nil
}

func (f *blockingASA) Games(context.Context, asa.GamesFilters) ([]asa.Game, error) {
	close(f.gamesStarted)
	<-f.release
	return []asa.Game{testGame("game-1", "FullTime", ptr(1), ptr(0))}, nil
}

func (f *blockingASA) GameXGoals(context.Context, asa.XGoalsFilters) ([]asa.GameXGoals, error) {
	close(f.xgStarted)
	<-f.release
	return []asa.GameXGoals{{
		GameID:         "game-1",
		HomeTeamID:     "home",
		AwayTeamID:     "away",
		HomeTeamXGoals: 1.2,
		AwayTeamXGoals: 0.6,
	}}, nil
}

type traceASA struct {
	fakeASA
	order *[]string
	xg    []asa.GameXGoals
}

func (f *traceASA) GameXGoals(context.Context, asa.XGoalsFilters) ([]asa.GameXGoals, error) {
	*f.order = append(*f.order, "xg")
	return f.xg, nil
}

type orderedRefresher struct {
	order  *[]string
	label  string
	forced *bool
}

func (r orderedRefresher) Refresh(_ context.Context, _ cache.SyncRun, _ []cache.Team, _ []cache.Game, force bool) (bool, error) {
	*r.order = append(*r.order, r.label)
	if r.forced != nil {
		*r.forced = force
	}
	return true, nil
}

func (f *fakeASA) Teams(context.Context, asa.TeamsFilters) ([]asa.Team, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.teamsCalls++
	if f.teamsErr != nil {
		return nil, f.teamsErr
	}
	return f.teams, nil
}

func (f *fakeASA) Games(_ context.Context, filters asa.GamesFilters) ([]asa.Game, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gamesCalls++
	f.gamesFilters = filters
	if filters.GameID != "" {
		if f.targetCalls == nil {
			f.targetCalls = make(map[string]int)
		}
		f.targetCalls[filters.GameID]++
		if err := f.targetErrors[filters.GameID]; err != nil {
			return nil, err
		}
		return f.targetGames[filters.GameID], nil
	}
	if f.gamesErr != nil {
		return nil, f.gamesErr
	}
	if len(f.gameResponses) > 0 {
		games := f.gameResponses[0]
		f.gameResponses = f.gameResponses[1:]
		return games, nil
	}
	return f.games, nil
}

func (f *fakeASA) targetGameCalls(id string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.targetCalls[id]
}

func testTeams() []asa.Team {
	return []asa.Team{
		{TeamID: "home", TeamName: "Home FC", TeamShortName: "Home", TeamAbbreviation: "HOM", RawJSON: `{"team_id":"home","extra":"kept"}`},
		{TeamID: "away", TeamName: "Away FC", TeamShortName: "Away", TeamAbbreviation: "AWY", RawJSON: `{"team_id":"away"}`},
	}
}

func testGame(id, status string, homeScore, awayScore *int) asa.Game {
	return asa.Game{
		GameID:         id,
		DateTimeUTC:    "2024-11-03 22:30:00 UTC",
		HomeScore:      homeScore,
		AwayScore:      awayScore,
		HomeTeamID:     "home",
		AwayTeamID:     "away",
		SeasonName:     "2024",
		Status:         status,
		LastUpdatedUTC: "2024-11-07 15:57:43 UTC",
		RawJSON:        `{"game_id":"` + id + `","extra":"kept"}`,
	}
}

func ptr(value int) *int {
	return &value
}
