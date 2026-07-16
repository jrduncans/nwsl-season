package syncer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	stdsync "sync"
	"time"

	"github.com/jrduncans/nwsl-season/internal/asa"
	"github.com/jrduncans/nwsl-season/internal/cache"
)

var runMu stdsync.Mutex

const allGameStatuses = "Abandoned,FullTime,PreMatch"

// ASAClient is the ASA surface required by the sync service.
type ASAClient interface {
	Teams(context.Context, asa.TeamsFilters) ([]asa.Team, error)
	Games(context.Context, asa.GamesFilters) ([]asa.Game, error)
}
type xgASAClient interface {
	GameXGoals(context.Context, asa.XGoalsFilters) ([]asa.GameXGoals, error)
}
type xgStore interface {
	ReplaceGameXG(context.Context, string, string, []cache.Game, []cache.GameXG, time.Time) (cache.XGSyncRun, error)
	RecordXGFailure(context.Context, string, string, time.Time, error) error
}

// Store is the cache surface required by the sync service.
type Store interface {
	ReplaceSeason(context.Context, string, string, []cache.Team, []cache.Game, time.Time) (cache.SyncRun, error)
	RecordFailure(context.Context, string, string, time.Time, error) error
	LastSuccess(context.Context, string, string) (*cache.SyncRun, error)
	LastAttempt(context.Context, string, string) (*cache.SyncRun, error)
	TryAcquireSyncLease(context.Context, string, string, time.Time) (bool, error)
	ReleaseSyncLease(context.Context, string, string) error
}

// Service refreshes the persistent cache from ASA.
type Service struct {
	ASA           ASAClient
	Store         Store
	Qualification QualificationRefresher
}

// QualificationRefresher runs after the durable fixture transaction. It is
// intentionally separate from Store so a qualification failure cannot relabel
// a successful fixture refresh.
type QualificationRefresher interface {
	Refresh(context.Context, cache.SyncRun, []cache.Team, []cache.Game) error
}

// RunOptions configures one sync run.
type RunOptions struct {
	Season                 string
	Stage                  string
	MinimumAttemptInterval time.Duration
	Force                  bool
}

// Run fetches one complete ASA season/stage and atomically stores it.
func (s Service) Run(ctx context.Context, options RunOptions) (cache.SyncRun, error) {
	runMu.Lock()
	defer runMu.Unlock()

	startedAt := time.Now().UTC()
	if s.ASA == nil {
		return cache.SyncRun{}, errors.New("sync ASA client is required")
	}
	if s.Store == nil {
		return cache.SyncRun{}, errors.New("sync store is required")
	}
	if strings.TrimSpace(options.Season) == "" {
		return cache.SyncRun{}, errors.New("sync season is required")
	}
	if strings.TrimSpace(options.Stage) == "" {
		return cache.SyncRun{}, errors.New("sync stage is required")
	}

	holder := fmt.Sprintf("%d-%d", os.Getpid(), startedAt.UnixNano())
	acquired, err := s.Store.TryAcquireSyncLease(ctx, leaseKey(options), holder, leaseExpiry(ctx, startedAt))
	if err != nil {
		return cache.SyncRun{}, err
	}
	if !acquired {
		return cache.SyncRun{}, cache.ErrSyncInProgress
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Store.ReleaseSyncLease(releaseCtx, leaseKey(options), holder)
	}()

	if !options.Force && options.MinimumAttemptInterval > 0 {
		run, err := s.Store.LastAttempt(ctx, options.Season, options.Stage)
		if err != nil {
			return cache.SyncRun{}, fmt.Errorf("check recent sync: %w", err)
		}
		if run != nil && !run.FinishedAt.Add(options.MinimumAttemptInterval).Before(startedAt) {
			run.Skipped = true
			return *run, nil
		}
	}

	teams, err := s.ASA.Teams(ctx, asa.TeamsFilters{})
	if err != nil {
		return cache.SyncRun{}, s.fail(ctx, options, startedAt, fmt.Errorf("fetch teams: %w", err))
	}

	games, err := s.ASA.Games(ctx, asa.GamesFilters{
		SeasonName: options.Season,
		StageName:  options.Stage,
		Status:     allGameStatuses,
	})
	if err != nil {
		return cache.SyncRun{}, s.fail(ctx, options, startedAt, fmt.Errorf("fetch games: %w", err))
	}

	if err := validate(options, teams, games); err != nil {
		return cache.SyncRun{}, s.fail(ctx, options, startedAt, err)
	}

	cacheTeams, err := mapTeams(teams)
	if err != nil {
		return cache.SyncRun{}, s.fail(ctx, options, startedAt, err)
	}
	cacheGames, err := mapGames(options, games)
	if err != nil {
		return cache.SyncRun{}, s.fail(ctx, options, startedAt, err)
	}

	run, err := s.Store.ReplaceSeason(ctx, options.Season, options.Stage, cacheTeams, cacheGames, startedAt)
	if err != nil {
		return cache.SyncRun{}, s.fail(ctx, options, startedAt, err)
	}
	if s.Qualification != nil {
		derivedCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		err := s.Qualification.Refresh(derivedCtx, run, cacheTeams, cacheGames)
		cancel()
		if err != nil {
			run.QualificationError = err.Error()
		}
	}
	// Fixture success is independent from xG. Keep the good fixture snapshot if
	// xG is delayed, malformed, or unavailable, and audit that separately.
	xgClient, hasXGClient := s.ASA.(xgASAClient)
	xgCache, hasXGCache := s.Store.(xgStore)
	if !hasXGClient || !hasXGCache {
		return run, nil
	}
	xg, err := xgClient.GameXGoals(ctx, asa.XGoalsFilters{SeasonName: options.Season, StageName: options.Stage})
	if err != nil {
		return s.xgWarning(ctx, xgCache, options, startedAt, run, fmt.Errorf("fetch game xG: %w", err)), nil
	}
	values, err := mapXGoals(xg)
	if err != nil {
		return s.xgWarning(ctx, xgCache, options, startedAt, run, err), nil
	}
	xgRun, err := xgCache.ReplaceGameXG(ctx, options.Season, options.Stage, cacheGames, values, startedAt)
	if err != nil {
		return s.xgWarning(ctx, xgCache, options, startedAt, run, err), nil
	}
	run.XGRun = &xgRun
	return run, nil
}

func (s Service) xgWarning(ctx context.Context, store xgStore, options RunOptions, startedAt time.Time, run cache.SyncRun, cause error) cache.SyncRun {
	recordCtx := ctx
	if ctx.Err() != nil {
		var cancel context.CancelFunc
		recordCtx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
	}
	if err := store.RecordXGFailure(recordCtx, options.Season, options.Stage, startedAt, cause); err != nil {
		run.XGError = cause.Error() + "; additionally failed to record xG failure: " + err.Error()
	} else {
		run.XGError = cause.Error()
	}
	return run
}

func (s Service) fail(ctx context.Context, options RunOptions, startedAt time.Time, cause error) error {
	recordCtx := ctx
	if ctx.Err() != nil {
		var cancel context.CancelFunc
		recordCtx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
	}
	if err := s.Store.RecordFailure(recordCtx, options.Season, options.Stage, startedAt, cause); err != nil {
		return fmt.Errorf("%w; additionally failed to record sync failure: %v", cause, err)
	}
	return cause
}

func leaseKey(options RunOptions) string {
	return options.Season + "\x00" + options.Stage
}

func leaseExpiry(ctx context.Context, startedAt time.Time) time.Time {
	if deadline, ok := ctx.Deadline(); ok && deadline.After(startedAt) {
		return deadline
	}
	return startedAt.Add(time.Minute)
}

func validate(options RunOptions, teams []asa.Team, games []asa.Game) error {
	if len(teams) == 0 {
		return errors.New("validate ASA response: teams response is empty")
	}
	if len(games) == 0 {
		return errors.New("validate ASA response: games response is empty")
	}

	teamIDs := make(map[string]struct{}, len(teams))
	for i, team := range teams {
		if strings.TrimSpace(team.TeamID) == "" {
			return fmt.Errorf("validate ASA response: team %d is missing team_id", i)
		}
		teamIDs[team.TeamID] = struct{}{}
	}

	gameIDs := make(map[string]struct{}, len(games))
	for i, game := range games {
		label := fmt.Sprintf("game %d", i)
		if game.GameID != "" {
			label = fmt.Sprintf("game %q", game.GameID)
		}
		if strings.TrimSpace(game.GameID) == "" {
			return fmt.Errorf("validate ASA response: %s is missing game_id", label)
		}
		if _, exists := gameIDs[game.GameID]; exists {
			return fmt.Errorf("validate ASA response: duplicate game_id %q", game.GameID)
		}
		gameIDs[game.GameID] = struct{}{}
		if strings.TrimSpace(game.SeasonName) == "" {
			return fmt.Errorf("validate ASA response: %s is missing season_name", label)
		}
		if game.SeasonName != options.Season {
			return fmt.Errorf("validate ASA response: %s season_name = %q, want %q", label, game.SeasonName, options.Season)
		}
		if strings.TrimSpace(game.DateTimeUTC) == "" {
			return fmt.Errorf("validate ASA response: %s is missing date_time_utc", label)
		}
		if strings.TrimSpace(game.Status) == "" {
			return fmt.Errorf("validate ASA response: %s is missing status", label)
		}
		if _, ok := teamIDs[game.HomeTeamID]; !ok {
			return fmt.Errorf("validate ASA response: %s references unknown home team %q", label, game.HomeTeamID)
		}
		if _, ok := teamIDs[game.AwayTeamID]; !ok {
			return fmt.Errorf("validate ASA response: %s references unknown away team %q", label, game.AwayTeamID)
		}
		if game.HomeScore != nil && *game.HomeScore < 0 {
			return fmt.Errorf("validate ASA response: %s has negative home score", label)
		}
		if game.AwayScore != nil && *game.AwayScore < 0 {
			return fmt.Errorf("validate ASA response: %s has negative away score", label)
		}
		if game.Status == "FullTime" && (game.HomeScore == nil || game.AwayScore == nil) {
			return fmt.Errorf("validate ASA response: %s is FullTime without both scores", label)
		}
	}
	return nil
}

func mapTeams(teams []asa.Team) ([]cache.Team, error) {
	cacheTeams := make([]cache.Team, 0, len(teams))
	for _, team := range teams {
		raw := team.RawJSON
		if raw == "" {
			marshaled, err := json.Marshal(team)
			if err != nil {
				return nil, fmt.Errorf("marshal raw team %q: %w", team.TeamID, err)
			}
			raw = string(marshaled)
		}
		cacheTeams = append(cacheTeams, cache.Team{
			ASAID:        team.TeamID,
			Name:         team.TeamName,
			ShortName:    team.TeamShortName,
			Abbreviation: team.TeamAbbreviation,
			RawJSON:      raw,
		})
	}
	return cacheTeams, nil
}

func mapGames(options RunOptions, games []asa.Game) ([]cache.Game, error) {
	cacheGames := make([]cache.Game, 0, len(games))
	for _, game := range games {
		raw := game.RawJSON
		if raw == "" {
			marshaled, err := json.Marshal(game)
			if err != nil {
				return nil, fmt.Errorf("marshal raw game %q: %w", game.GameID, err)
			}
			raw = string(marshaled)
		}
		cacheGames = append(cacheGames, cache.Game{
			ASAID:          game.GameID,
			Season:         options.Season,
			Stage:          options.Stage,
			KickoffUTC:     game.DateTimeUTC,
			Status:         game.Status,
			HomeTeamID:     game.HomeTeamID,
			AwayTeamID:     game.AwayTeamID,
			HomeScore:      nullInt(game.HomeScore),
			AwayScore:      nullInt(game.AwayScore),
			Matchday:       nullInt(game.Matchday),
			LastUpdatedUTC: game.LastUpdatedUTC,
			RawJSON:        raw,
		})
	}
	return cacheGames, nil
}

func mapXGoals(values []asa.GameXGoals) ([]cache.GameXG, error) {
	result := make([]cache.GameXG, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value.GameID) == "" || strings.TrimSpace(value.HomeTeamID) == "" || strings.TrimSpace(value.AwayTeamID) == "" {
			return nil, fmt.Errorf("validate ASA xG response: required identity is missing")
		}
		if math.IsNaN(value.HomeTeamXGoals) || math.IsInf(value.HomeTeamXGoals, 0) || value.HomeTeamXGoals < 0 || math.IsNaN(value.AwayTeamXGoals) || math.IsInf(value.AwayTeamXGoals, 0) || value.AwayTeamXGoals < 0 {
			return nil, fmt.Errorf("validate ASA xG response: invalid xG for game %q", value.GameID)
		}
		raw := value.RawJSON
		if raw == "" {
			encoded, err := json.Marshal(value)
			if err != nil {
				return nil, err
			}
			raw = string(encoded)
		}
		result = append(result, cache.GameXG{GameID: value.GameID, Availability: cache.XGAvailable, HomeTeamID: value.HomeTeamID, AwayTeamID: value.AwayTeamID, HomeXG: sql.NullFloat64{Float64: value.HomeTeamXGoals, Valid: true}, AwayXG: sql.NullFloat64{Float64: value.AwayTeamXGoals, Valid: true}, RawJSON: raw})
	}
	return result, nil
}

func nullInt(value *int) sql.NullInt64 {
	if value == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*value), Valid: true}
}
