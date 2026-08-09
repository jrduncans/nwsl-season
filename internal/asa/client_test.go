package asa

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestGamesDecodesResponse(t *testing.T) {
	fixture, err := os.ReadFile("testdata/games.json")
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	}))
	defer server.Close()

	client := Client{BaseURL: server.URL, HTTPClient: server.Client()}

	games, err := client.Games(context.Background(), GamesFilters{
		SeasonName: "2024",
		StageName:  "Regular Season",
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(games) != 2 {
		t.Fatalf("len(games) = %d, want 2", len(games))
	}
	if games[0].GameID != "wvq9w4lNMW" {
		t.Errorf("first game ID = %q, want %q", games[0].GameID, "wvq9w4lNMW")
	}
	if games[0].HomeScore == nil || *games[0].HomeScore != 3 {
		t.Fatalf("first home score = %v, want 3", games[0].HomeScore)
	}
	if games[1].HomeScore != nil {
		t.Fatalf("second home score = %v, want nil", *games[1].HomeScore)
	}
	if games[1].Attendance != nil {
		t.Fatalf("second attendance = %v, want nil", *games[1].Attendance)
	}
}

func TestGamesCreatesDownstreamHTTPSpan(t *testing.T) {
	fixture, err := os.ReadFile("testdata/games.json")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(fixture)
	}))
	defer server.Close()

	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		_ = provider.Shutdown(context.Background())
	})

	ctx, parent := otel.Tracer("test").Start(context.Background(), "sync.run")
	_, err = (Client{BaseURL: server.URL, HTTPClient: server.Client()}).Games(ctx, GamesFilters{SeasonName: "2026"})
	parent.End()
	if err != nil {
		t.Fatal(err)
	}

	for _, span := range exporter.GetSpans() {
		if span.SpanKind == trace.SpanKindClient {
			if span.Name != "HTTP GET /nwsl/games" {
				t.Errorf("client span name = %q, want HTTP GET /nwsl/games", span.Name)
			}
			return
		}
	}
	t.Fatal("ASA request did not create a client span")
}

func TestGamesSendsQueryParameters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/nwsl/games" {
			t.Errorf("path = %q, want %q", r.URL.Path, "/nwsl/games")
		}

		query := r.URL.Query()
		want := map[string]string{
			"season_name": "2024",
			"stage_name":  "Regular Season",
			"status":      "FullTime",
			"game_id":     "game-1",
			"team_id":     "team-1",
			"start_date":  "2024-01-01",
			"end_date":    "2024-12-31",
		}
		for name, value := range want {
			if got := query.Get(name); got != value {
				t.Errorf("query %s = %q, want %q", name, got, value)
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	client := Client{BaseURL: server.URL, HTTPClient: server.Client()}

	_, err := client.Games(context.Background(), GamesFilters{
		GameID:     "game-1",
		TeamID:     "team-1",
		SeasonName: "2024",
		StageName:  "Regular Season",
		Status:     "FullTime",
		StartDate:  "2024-01-01",
		EndDate:    "2024-12-31",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTeamsDecodesResponse(t *testing.T) {
	fixture, err := os.ReadFile("testdata/teams.json")
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	}))
	defer server.Close()

	client := Client{BaseURL: server.URL, HTTPClient: server.Client()}

	teams, err := client.Teams(context.Background(), TeamsFilters{})
	if err != nil {
		t.Fatal(err)
	}

	if len(teams) != 4 {
		t.Fatalf("len(teams) = %d, want 4", len(teams))
	}
	if teams[0].TeamID != "7VqG1lYMvW" {
		t.Errorf("first team ID = %q, want %q", teams[0].TeamID, "7VqG1lYMvW")
	}
	if teams[0].TeamName != "Gotham FC" {
		t.Errorf("first team name = %q, want Gotham FC", teams[0].TeamName)
	}
}

func TestTeamsSendsQueryParameters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/nwsl/teams" {
			t.Errorf("path = %q, want %q", r.URL.Path, "/nwsl/teams")
		}
		if got := r.URL.Query().Get("team_id"); got != "team-1,team-2" {
			t.Errorf("team_id query = %q, want team-1,team-2", got)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	client := Client{BaseURL: server.URL, HTTPClient: server.Client()}

	_, err := client.Teams(context.Background(), TeamsFilters{TeamID: "team-1,team-2"})
	if err != nil {
		t.Fatal(err)
	}
}

func TestGameXGoalsDecodesExpectedPoints(t *testing.T) {
	fixture, err := os.ReadFile("testdata/game_xgoals.json")
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/nwsl/games/xgoals" {
			t.Errorf("path = %q, want %q", r.URL.Path, "/nwsl/games/xgoals")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	}))
	defer server.Close()

	values, err := (Client{BaseURL: server.URL, HTTPClient: server.Client()}).GameXGoals(context.Background(), XGoalsFilters{SeasonName: "2025", StageName: "Regular Season"})
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || values[0].HomeXPoints == nil || values[0].AwayXPoints == nil {
		t.Fatalf("xG values = %+v, want one game with expected points", values)
	}
	if *values[0].HomeXPoints != 2.47 || *values[0].AwayXPoints != .367 {
		t.Fatalf("expected points = %.3f / %.3f, want 2.470 / 0.367", *values[0].HomeXPoints, *values[0].AwayXPoints)
	}
}

func TestTeamsReturnsErrorForNon2xx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad upstream", http.StatusBadGateway)
	}))
	defer server.Close()

	client := Client{BaseURL: server.URL, HTTPClient: server.Client(), RetryDelays: []time.Duration{}}

	_, err := client.Teams(context.Background(), TeamsFilters{})
	if err == nil {
		t.Fatal("err = nil, want error")
	}
	if !strings.Contains(err.Error(), "asa teams") {
		t.Errorf("error = %q, want operation", err.Error())
	}
	if !strings.Contains(err.Error(), "502") {
		t.Errorf("error = %q, want HTTP status", err.Error())
	}
}

func TestTeamsReturnsErrorForMalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"not": "an array"`))
	}))
	defer server.Close()

	client := Client{BaseURL: server.URL, HTTPClient: server.Client()}

	_, err := client.Teams(context.Background(), TeamsFilters{})
	if err == nil {
		t.Fatal("err = nil, want error")
	}
	if !strings.Contains(err.Error(), "decode response") {
		t.Errorf("error = %q, want decode response", err.Error())
	}
}

func TestGamesReturnsErrorForNon2xx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, strings.Repeat("x", maxErrorBodyBytes*2), http.StatusBadGateway)
	}))
	defer server.Close()

	client := Client{BaseURL: server.URL, HTTPClient: server.Client(), RetryDelays: []time.Duration{}}

	_, err := client.Games(context.Background(), GamesFilters{})
	if err == nil {
		t.Fatal("err = nil, want error")
	}

	message := err.Error()
	if !strings.Contains(message, "asa games") {
		t.Errorf("error = %q, want operation", message)
	}
	if !strings.Contains(message, "502") {
		t.Errorf("error = %q, want HTTP status", message)
	}
	if len(message) > maxErrorBodyBytes+200 {
		t.Errorf("error length = %d, want capped body", len(message))
	}
}

func TestGamesReturnsErrorForMalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"not": "an array"`))
	}))
	defer server.Close()

	client := Client{BaseURL: server.URL, HTTPClient: server.Client()}

	_, err := client.Games(context.Background(), GamesFilters{})
	if err == nil {
		t.Fatal("err = nil, want error")
	}
	if !strings.Contains(err.Error(), "decode response") {
		t.Errorf("error = %q, want decode response", err.Error())
	}
}

func TestGamesReturnsContextError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	client := Client{BaseURL: "http://127.0.0.1:1", HTTPClient: &http.Client{Timeout: time.Second}}

	_, err := client.Games(ctx, GamesFilters{})
	if err == nil {
		t.Fatal("err = nil, want error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestClientRetriesTransientHTTPStatuses(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) < 3 {
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		_ = provider.Shutdown(context.Background())
	})

	client := Client{
		BaseURL:     server.URL,
		HTTPClient:  server.Client(),
		RetryDelays: []time.Duration{0, 0},
	}
	ctx, parent := otel.Tracer("test").Start(context.Background(), "sync.run")
	_, err := client.Teams(ctx, TeamsFilters{})
	parent.End()
	if err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("requests = %d, want 3", got)
	}
	clientSpans := 0
	for _, span := range exporter.GetSpans() {
		if span.SpanKind == trace.SpanKindClient {
			clientSpans++
		}
	}
	if clientSpans != 3 {
		t.Fatalf("downstream HTTP spans = %d, want one for each of 3 attempts", clientSpans)
	}
}

func TestClientDoesNotRetryNonTransientHTTPStatus(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer server.Close()

	client := Client{
		BaseURL:     server.URL,
		HTTPClient:  server.Client(),
		RetryDelays: []time.Duration{0, 0},
	}
	if _, err := client.Teams(context.Background(), TeamsFilters{}); err == nil {
		t.Fatal("err = nil, want HTTP error")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("requests = %d, want 1", got)
	}
}

func TestClientRetriesTransportErrors(t *testing.T) {
	var calls atomic.Int32
	client := Client{
		BaseURL: "https://example.test",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if calls.Add(1) < 3 {
				return nil, errors.New("temporary transport failure")
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`[]`)),
				Request:    request,
			}, nil
		})},
		RetryDelays: []time.Duration{0, 0},
	}

	if _, err := client.Teams(context.Background(), TeamsFilters{}); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("requests = %d, want 3", got)
	}
}

func TestASARetryPolicyAddsRequestTimeoutAndTooEarly(t *testing.T) {
	for _, status := range []int{http.StatusRequestTimeout, http.StatusTooEarly} {
		retry, err := asaRetryPolicy(context.Background(), &http.Response{StatusCode: status}, nil)
		if err != nil || !retry {
			t.Errorf("status %d: retry = %t, err = %v; want retry", status, retry, err)
		}
	}
}

func TestConfiguredBackoffHonorsLongerRetryAfter(t *testing.T) {
	response := &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Header:     http.Header{"Retry-After": []string{"2"}},
	}
	if got := configuredBackoff([]time.Duration{250 * time.Millisecond})(0, 0, 0, response); got != 2*time.Second {
		t.Fatalf("backoff = %s, want 2s Retry-After", got)
	}
}

func TestSuccessResponsesRejectOversizedBodies(t *testing.T) {
	tests := []struct {
		name  string
		limit int64
		fetch func(Client) error
	}{
		{
			name:  "teams",
			limit: maxTeamsResponseBytes,
			fetch: func(client Client) error {
				_, err := client.Teams(context.Background(), TeamsFilters{})
				return err
			},
		},
		{
			name:  "games",
			limit: maxGamesResponseBytes,
			fetch: func(client Client) error {
				_, err := client.Games(context.Background(), GamesFilters{})
				return err
			},
		},
		{
			name:  "xgoals",
			limit: maxXGoalsResponseBytes,
			fetch: func(client Client) error {
				_, err := client.GameXGoals(context.Background(), XGoalsFilters{})
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte("[]"))
				_, _ = w.Write([]byte(strings.Repeat(" ", int(test.limit))))
			}))
			defer server.Close()

			err := test.fetch(Client{BaseURL: server.URL, HTTPClient: server.Client()})
			if !errors.Is(err, errResponseBodyTooLarge) {
				t.Fatalf("err = %v, want oversized-body error", err)
			}
		})
	}
}

func TestSuccessResponsesRejectExcessiveRows(t *testing.T) {
	tests := []struct {
		name  string
		rows  int
		fetch func(Client) error
	}{
		{
			name: "teams",
			rows: maxTeamRows,
			fetch: func(client Client) error {
				_, err := client.Teams(context.Background(), TeamsFilters{})
				return err
			},
		},
		{
			name: "games",
			rows: maxGameRows,
			fetch: func(client Client) error {
				_, err := client.Games(context.Background(), GamesFilters{})
				return err
			},
		},
		{
			name: "xgoals",
			rows: maxXGoalRows,
			fetch: func(client Client) error {
				_, err := client.GameXGoals(context.Background(), XGoalsFilters{})
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := "[" + strings.TrimSuffix(strings.Repeat("{},", test.rows+1), ",") + "]"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()

			err := test.fetch(Client{BaseURL: server.URL, HTTPClient: server.Client()})
			if err == nil || !strings.Contains(err.Error(), "more than") {
				t.Fatalf("err = %v, want row-limit error", err)
			}
		})
	}
}

func TestSuccessResponsesRejectTrailingContent(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		fetch func(Client) error
	}{
		{
			name: "trailing JSON value",
			body: "[] {}",
			fetch: func(client Client) error {
				_, err := client.Teams(context.Background(), TeamsFilters{})
				return err
			},
		},
		{
			name: "trailing non-whitespace",
			body: "[] trailing",
			fetch: func(client Client) error {
				_, err := client.Teams(context.Background(), TeamsFilters{})
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()

			err := test.fetch(Client{BaseURL: server.URL, HTTPClient: server.Client()})
			if err == nil || !strings.Contains(err.Error(), "trailing") {
				t.Fatalf("err = %v, want trailing-content error", err)
			}
		})
	}
}

func TestCheckedInFixturesFitResponseLimits(t *testing.T) {
	tests := []struct {
		path  string
		limit int64
	}{
		{path: "testdata/teams.json", limit: maxTeamsResponseBytes},
		{path: "testdata/games.json", limit: maxGamesResponseBytes},
		{path: "testdata/game_xgoals.json", limit: maxXGoalsResponseBytes},
	}

	for _, test := range tests {
		info, err := os.Stat(test.path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() > test.limit/2 {
			t.Errorf("%s is %d bytes, want at most half of the %d-byte response limit", test.path, info.Size(), test.limit)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
