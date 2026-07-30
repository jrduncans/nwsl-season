package asa

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

const (
	DefaultBaseURL    = "https://app.americansocceranalysis.com/api/v1"
	maxErrorBodyBytes = 1024

	// These allow ample room for a full NWSL season while bounding memory use
	// if an upstream response is malformed or unexpectedly large.
	maxTeamsResponseBytes  int64 = 1 << 20 // 1 MiB
	maxGamesResponseBytes  int64 = 4 << 20 // 4 MiB
	maxXGoalsResponseBytes int64 = 2 << 20 // 2 MiB
	maxTeamRows                  = 64
	maxGameRows                  = 512
	maxXGoalRows                 = 512
)

var errResponseBodyTooLarge = errors.New("response body exceeds size limit")

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

// XGoalsFilters contains the supported /nwsl/games/xgoals query parameters.
type XGoalsFilters struct {
	SeasonName string
	StageName  string
}

// GameXGoals is ASA's game-level xG response. The field names are captured
// from the live endpoint; RawJSON preserves the complete source object.
type GameXGoals struct {
	GameID         string   `json:"game_id"`
	HomeTeamID     string   `json:"home_team_id"`
	AwayTeamID     string   `json:"away_team_id"`
	HomeTeamXGoals float64  `json:"home_team_xgoals"`
	AwayTeamXGoals float64  `json:"away_team_xgoals"`
	HomeXPoints    *float64 `json:"home_xpoints"`
	AwayXPoints    *float64 `json:"away_xpoints"`
	RawJSON        string   `json:"-"`
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

// GameXGoals fetches ASA's team-model game expected-goals observations.
func (c Client) GameXGoals(ctx context.Context, filters XGoalsFilters) ([]GameXGoals, error) {
	const op = "asa game xgoals"
	endpoint, err := c.resourceURL("/nwsl/games/xgoals", func(query url.Values) {
		addQuery(query, "season_name", filters.SeasonName)
		addQuery(query, "stage_name", filters.StageName)
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
	values, err := decodeXGoals(response.Body)
	if err != nil {
		return nil, fmt.Errorf("%s: decode response: %w", op, err)
	}
	return values, nil
}

func decodeGames(body io.Reader) ([]Game, error) {
	return decodeArray(body, maxGamesResponseBytes, maxGameRows, func(value *Game, raw string) {
		value.RawJSON = raw
	})
}

func decodeTeams(body io.Reader) ([]Team, error) {
	return decodeArray(body, maxTeamsResponseBytes, maxTeamRows, func(value *Team, raw string) {
		value.RawJSON = raw
	})
}

func decodeXGoals(body io.Reader) ([]GameXGoals, error) {
	return decodeArray(body, maxXGoalsResponseBytes, maxXGoalRows, func(value *GameXGoals, raw string) {
		value.RawJSON = raw
	})
}

// decodeArray decodes one JSON array, retaining each source object for cache
// provenance. It streams elements so the row cap is enforced before a large
// array can be materialized in memory.
func decodeArray[T any](body io.Reader, maxBytes int64, maxRows int, setRaw func(*T, string)) ([]T, error) {
	decoder := json.NewDecoder(&responseBodyLimitReader{reader: body, remaining: maxBytes})

	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '[' {
		return nil, fmt.Errorf("expected top-level JSON array")
	}

	values := make([]T, 0)
	for decoder.More() {
		if len(values) == maxRows {
			return nil, fmt.Errorf("response has more than %d rows", maxRows)
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, err
		}

		var value T
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		setRaw(&value, string(raw))
		values = append(values, value)
	}

	token, err = decoder.Token()
	if err != nil {
		return nil, err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != ']' {
		return nil, fmt.Errorf("expected end of JSON array")
	}

	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("unexpected trailing JSON value")
		}
		return nil, fmt.Errorf("invalid trailing response data: %w", err)
	}

	return values, nil
}

// responseBodyLimitReader reports a distinct error only after proving that
// more than the configured number of bytes are present. This keeps bodies
// exactly at the limit valid while rejecting oversized responses.
type responseBodyLimitReader struct {
	reader    io.Reader
	remaining int64
}

func (r *responseBodyLimitReader) Read(p []byte) (int, error) {
	if r.remaining > 0 {
		if int64(len(p)) > r.remaining {
			p = p[:r.remaining]
		}
		n, err := r.reader.Read(p)
		r.remaining -= int64(n)
		return n, err
	}

	var probe [1]byte
	n, err := r.reader.Read(probe[:])
	if n > 0 {
		return 0, errResponseBodyTooLarge
	}
	return 0, err
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
	base := c.HTTPClient
	if base == nil {
		base = http.DefaultClient
	}
	// Keep HTTP tracing at the ASA boundary so scheduled and command-line
	// syncs both expose the same downstream calls. Copying preserves caller
	// settings such as timeout and test-server TLS configuration.
	client := *base
	transport := base.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	client.Transport = otelhttp.NewTransport(transport)
	return &client
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
