package asa

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
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

	client := Client{BaseURL: server.URL, HTTPClient: server.Client()}

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

	client := Client{BaseURL: server.URL, HTTPClient: server.Client()}

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
