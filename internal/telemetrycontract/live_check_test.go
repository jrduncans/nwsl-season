package telemetrycontract_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/app"
	"github.com/jrduncans/nwsl-season/internal/asa"
	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/syncer"
	"github.com/jrduncans/nwsl-season/internal/telemetry"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

const liveCheckEnv = "NWSL_TELEMETRY_LIVE_CHECK"

// TestRuntimeTelemetryMatchesRegistry drives representative production
// instrumentation through Weaver Live Check. The ordinary Go suite skips it
// because it requires the development-only Collector bridge started by
// `make telemetry-live-check`.
func TestRuntimeTelemetryMatchesRegistry(t *testing.T) {
	if os.Getenv(liveCheckEnv) != "1" {
		t.Skip("set " + liveCheckEnv + "=1 through make telemetry-live-check")
	}
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" {
		t.Fatal("OTEL_EXPORTER_OTLP_ENDPOINT is required")
	}

	t.Setenv("HONEYCOMB_API_KEY", "")
	t.Setenv("HONEYCOMB_API_ENDPOINT", "")
	t.Setenv("HONEYCOMB_METRICS_DATASET", "")
	t.Setenv("OTEL_SERVICE_NAME", "nwsl-season-live-check")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "deployment.environment.name=live-check,service.instance.id=contract-test")
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf")
	t.Setenv("OTEL_TRACES_EXPORTER", "otlp")
	t.Setenv("OTEL_LOGS_EXPORTER", "otlp")
	t.Setenv("OTEL_METRICS_EXPORTER", "otlp")

	providers, err := telemetry.Configure(context.Background(), nil, "nwsl-season-live-check")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := providers.Shutdown(shutdownCtx); err != nil {
			t.Errorf("shutdown telemetry providers: %v", err)
		}
	}()

	asaServer := newFakeASAServer(t)
	defer asaServer.Close()

	db, err := cache.Open(context.Background(), filepath.Join(t.TempDir(), "cache.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("close cache: %v", err)
		}
	}()

	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	service := syncer.Service{
		ASA: asa.Client{
			BaseURL:     asaServer.URL,
			HTTPClient:  asaServer.Client(),
			RetryDelays: []time.Duration{},
		},
		Store: db,
		Now: func() time.Time {
			now = now.Add(time.Second)
			return now
		},
	}
	run, err := service.Run(context.Background(), syncer.RunOptions{
		Season:     "2024",
		Stage:      "Regular Season",
		Trigger:    "live_check",
		Force:      true,
		SourceOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.GamesSeen != 2 || run.TeamsUpserted != 4 || run.XGRun == nil {
		t.Fatalf("unexpected sync result: %+v", run)
	}

	application := app.NewHandlerWithOptions(db, app.Options{
		CurrentSeason: "2024",
		Stage:         "Regular Season",
		Location:      time.UTC,
	})
	httpServer := httptest.NewServer(otelhttp.NewHandler(application, "HTTP server",
		otelhttp.WithSpanNameFormatter(func(_ string, request *http.Request) string {
			if request.Pattern != "" {
				return request.Pattern
			}
			return request.Method + " unknown_route"
		}),
	))
	defer httpServer.Close()

	response, err := httpServer.Client().Get(httpServer.URL + "/seasons/2024/regular-season/fixtures")
	if err != nil {
		t.Fatal(err)
	}
	_, copyErr := io.Copy(io.Discard, response.Body)
	closeErr := response.Body.Close()
	if copyErr != nil {
		t.Fatal(copyErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("fixtures response status = %d, want %d", response.StatusCode, http.StatusOK)
	}
}

func newFakeASAServer(t *testing.T) *httptest.Server {
	t.Helper()
	homeScore, awayScore, matchday, minutes := 2, 1, 1, 96
	homePoints, awayPoints := 2.2, 0.5
	teams := []asa.Team{
		{TeamID: "alpha", TeamName: "Alpha FC", TeamShortName: "Alpha", TeamAbbreviation: "ALP"},
		{TeamID: "bravo", TeamName: "Bravo FC", TeamShortName: "Bravo", TeamAbbreviation: "BRV"},
		{TeamID: "charlie", TeamName: "Charlie FC", TeamShortName: "Charlie", TeamAbbreviation: "CHA"},
		{TeamID: "delta", TeamName: "Delta FC", TeamShortName: "Delta", TeamAbbreviation: "DEL"},
	}
	games := []asa.Game{
		{
			GameID: "completed", DateTimeUTC: "2024-08-01 19:00:00 UTC",
			HomeScore: &homeScore, AwayScore: &awayScore,
			HomeTeamID: "alpha", AwayTeamID: "bravo", ExpandedMinutes: &minutes,
			SeasonName: "2024", Matchday: &matchday, Status: "FullTime",
			LastUpdatedUTC: "2024-08-01 22:00:00 UTC",
		},
		{
			GameID: "scheduled", DateTimeUTC: "2024-08-08 19:00:00 UTC",
			HomeTeamID: "charlie", AwayTeamID: "delta",
			SeasonName: "2024", Matchday: &matchday, Status: "PreMatch",
			LastUpdatedUTC: "2024-08-01 22:00:00 UTC",
		},
	}
	xg := []asa.GameXGoals{{
		GameID: "completed", HomeTeamID: "alpha", AwayTeamID: "bravo",
		HomeTeamXGoals: 1.8, AwayTeamXGoals: 0.9,
		HomeXPoints: &homePoints, AwayXPoints: &awayPoints,
	}}

	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		var value any
		switch request.URL.Path {
		case "/nwsl/teams":
			value = teams
		case "/nwsl/games":
			if got := request.URL.Query().Get("season_name"); got != "2024" {
				http.Error(writer, fmt.Sprintf("unexpected season %q", got), http.StatusBadRequest)
				return
			}
			value = games
		case "/nwsl/games/xgoals":
			value = xg
		default:
			http.NotFound(writer, request)
			return
		}
		if err := json.NewEncoder(writer).Encode(value); err != nil {
			t.Errorf("encode fake ASA response: %v", err)
		}
	}))
}
