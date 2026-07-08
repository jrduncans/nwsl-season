package asa

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	DefaultBaseURL    = "https://app.americansocceranalysis.com/api/v1"
	maxErrorBodyBytes = 1024
)

// Client fetches data from the American Soccer Analysis API.
type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

// GamesFilters contains the supported /nwsl/games query parameters.
type GamesFilters struct {
	GameID     string
	TeamID     string
	SeasonName string
	StageName  string
	Status     string
	StartDate  string
	EndDate    string
}

// TeamsFilters contains the supported /nwsl/teams query parameters.
type TeamsFilters struct {
	TeamID string
}

// Game is the ASA /nwsl/games wire representation.
type Game struct {
	GameID          string `json:"game_id"`
	DateTimeUTC     string `json:"date_time_utc"`
	HomeScore       *int   `json:"home_score"`
	AwayScore       *int   `json:"away_score"`
	HomeTeamID      string `json:"home_team_id"`
	AwayTeamID      string `json:"away_team_id"`
	RefereeID       string `json:"referee_id"`
	StadiumID       string `json:"stadium_id"`
	HomeManagerID   string `json:"home_manager_id"`
	AwayManagerID   string `json:"away_manager_id"`
	ExpandedMinutes *int   `json:"expanded_minutes"`
	SeasonName      string `json:"season_name"`
	Matchday        *int   `json:"matchday"`
	Attendance      *int   `json:"attendance"`
	KnockoutGame    bool   `json:"knockout_game"`
	Status          string `json:"status"`
	LastUpdatedUTC  string `json:"last_updated_utc"`
	RawJSON         string `json:"-"`
}

// Team is the ASA /nwsl/teams wire representation.
type Team struct {
	TeamID           string `json:"team_id"`
	TeamName         string `json:"team_name"`
	TeamShortName    string `json:"team_short_name"`
	TeamAbbreviation string `json:"team_abbreviation"`
	RawJSON          string `json:"-"`
}

// Games fetches NWSL games from ASA.
func (c Client) Games(ctx context.Context, filters GamesFilters) ([]Game, error) {
	const op = "asa games"

	endpoint, err := c.resourceURL("/nwsl/games", func(query url.Values) {
		addQuery(query, "game_id", filters.GameID)
		addQuery(query, "team_id", filters.TeamID)
		addQuery(query, "season_name", filters.SeasonName)
		addQuery(query, "stage_name", filters.StageName)
		addQuery(query, "status", filters.Status)
		addQuery(query, "start_date", filters.StartDate)
		addQuery(query, "end_date", filters.EndDate)
	})
	if err != nil {
		return nil, fmt.Errorf("%s: build request URL: %w", op, err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: build request: %w", op, err)
	}

	response, err := c.httpClient().Do(request)
	if err != nil {
		return nil, fmt.Errorf("%s: send request: %w", op, err)
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("%s: unexpected HTTP status %d: %s", op, response.StatusCode, limitedBody(response.Body))
	}

	games, err := decodeGames(response.Body)
	if err != nil {
		return nil, fmt.Errorf("%s: decode response: %w", op, err)
	}

	return games, nil
}

// Teams fetches NWSL teams from ASA.
func (c Client) Teams(ctx context.Context, filters TeamsFilters) ([]Team, error) {
	const op = "asa teams"

	endpoint, err := c.resourceURL("/nwsl/teams", func(query url.Values) {
		addQuery(query, "team_id", filters.TeamID)
	})
	if err != nil {
		return nil, fmt.Errorf("%s: build request URL: %w", op, err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: build request: %w", op, err)
	}

	response, err := c.httpClient().Do(request)
	if err != nil {
		return nil, fmt.Errorf("%s: send request: %w", op, err)
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("%s: unexpected HTTP status %d: %s", op, response.StatusCode, limitedBody(response.Body))
	}

	teams, err := decodeTeams(response.Body)
	if err != nil {
		return nil, fmt.Errorf("%s: decode response: %w", op, err)
	}

	return teams, nil
}

func decodeGames(body io.Reader) ([]Game, error) {
	var rawObjects []json.RawMessage
	if err := json.NewDecoder(body).Decode(&rawObjects); err != nil {
		return nil, err
	}

	values := make([]Game, 0, len(rawObjects))
	for _, raw := range rawObjects {
		var value Game
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		value.RawJSON = string(raw)
		values = append(values, value)
	}
	return values, nil
}

func decodeTeams(body io.Reader) ([]Team, error) {
	var rawObjects []json.RawMessage
	if err := json.NewDecoder(body).Decode(&rawObjects); err != nil {
		return nil, err
	}

	values := make([]Team, 0, len(rawObjects))
	for _, raw := range rawObjects {
		var value Team
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		value.RawJSON = string(raw)
		values = append(values, value)
	}
	return values, nil
}

func (c Client) resourceURL(path string, add func(url.Values)) (string, error) {
	baseURL := c.BaseURL
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}

	parsed, err := url.Parse(strings.TrimRight(baseURL, "/") + path)
	if err != nil {
		return "", err
	}

	query := parsed.Query()
	add(query)
	parsed.RawQuery = query.Encode()

	return parsed.String(), nil
}

func addQuery(query url.Values, name, value string) {
	if value != "" {
		query.Set(name, value)
	}
}

func (c Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

func limitedBody(body io.Reader) string {
	limited, err := io.ReadAll(io.LimitReader(body, maxErrorBodyBytes))
	if err != nil {
		return "read error body: " + err.Error()
	}
	text := strings.TrimSpace(string(limited))
	if text == "" {
		return "<empty body>"
	}
	return text
}
