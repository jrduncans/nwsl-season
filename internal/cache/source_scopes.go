package cache

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jrduncans/nwsl-season/internal/competition"
)

type SourceScopeRegistration string

const (
	SourceScopeCatalog     SourceScopeRegistration = "catalog"
	SourceScopeConfigured  SourceScopeRegistration = "configured"
	SourceScopeProvisional SourceScopeRegistration = "provisional"
	SourceScopeObserved    SourceScopeRegistration = "observed"
)

type SourceScopeLifecycle string

const (
	SourceScopeUpcoming  SourceScopeLifecycle = "upcoming"
	SourceScopeActive    SourceScopeLifecycle = "active"
	SourceScopeCompleted SourceScopeLifecycle = "completed"
)

type SourceScopeDiscovery string

const (
	SourceScopeUnknown      SourceScopeDiscovery = "unknown"
	SourceScopeNotPublished SourceScopeDiscovery = "not_published"
	SourceScopeAvailable    SourceScopeDiscovery = "available"
)

// SourceScope is a durable source identity retained by the loading plan.
type SourceScope struct {
	Season       string
	Stage        string
	Registration SourceScopeRegistration
	Lifecycle    SourceScopeLifecycle
	Discovery    SourceScopeDiscovery
	RegisteredAt time.Time
	UpdatedAt    time.Time
}

type sourceScopeSeed struct {
	season       string
	stage        string
	registration SourceScopeRegistration
}

type sourceScopeIdentity struct {
	season string
	stage  string
}

// EnsureSourceScopes records the configured, catalog, and rolling provisional
// source scopes. Its clock is caller-supplied so the seed set is deterministic.
func (c *DB) EnsureSourceScopes(ctx context.Context, configuredSeason, configuredStage string, now time.Time) ([]SourceScope, error) {
	if strings.TrimSpace(configuredSeason) == "" {
		return nil, errors.New("configured source scope season is blank")
	}
	if strings.TrimSpace(configuredStage) == "" {
		return nil, errors.New("configured source scope stage is blank")
	}
	if now.IsZero() {
		return nil, errors.New("source scope seed clock is zero")
	}
	now = now.UTC()

	seeds := sourceScopeSeeds(configuredSeason, configuredStage, now.Year())
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin source scope seed: %w", err)
	}
	defer rollback(tx)

	for _, seed := range seeds {
		existing, found, err := sourceScope(ctx, tx, seed.season, seed.stage)
		if err != nil {
			return nil, err
		}
		if !found {
			newScope := SourceScope{
				Season:       seed.season,
				Stage:        seed.stage,
				Registration: seed.registration,
				Lifecycle:    seedLifecycle(seed.season, now.Year()),
				Discovery:    SourceScopeUnknown,
				RegisteredAt: now,
				UpdatedAt:    now,
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO source_scopes (
				season, stage, registration, lifecycle, discovery, registered_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?)`,
				newScope.Season, newScope.Stage, newScope.Registration, newScope.Lifecycle, newScope.Discovery,
				formatTime(newScope.RegisteredAt), formatTime(newScope.UpdatedAt)); err != nil {
				return nil, fmt.Errorf("insert source scope %s %s: %w", seed.season, seed.stage, err)
			}
			continue
		}

		merged, changed := mergeSourceScope(existing, seed, now.Year())
		if !changed {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE source_scopes
			SET registration = ?, lifecycle = ?, updated_at = ?
			WHERE season = ? AND stage = ?`,
			merged.Registration, merged.Lifecycle, formatTime(now), merged.Season, merged.Stage); err != nil {
			return nil, fmt.Errorf("update source scope %s %s: %w", seed.season, seed.stage, err)
		}
	}
	if err := promoteRetainedUpcomingSourceScopes(ctx, tx, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit source scope seed: %w", err)
	}
	return c.SourceScopes(ctx)
}

func promoteRetainedUpcomingSourceScopes(ctx context.Context, tx *sql.Tx, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `UPDATE source_scopes
		SET lifecycle = CASE
				WHEN CAST(season AS INTEGER) < ? THEN ?
				ELSE ?
			END,
			updated_at = ?
		WHERE lifecycle = ?
			AND season GLOB '[0-9][0-9][0-9][0-9]'
			AND CAST(season AS INTEGER) <= ?`,
		now.Year(), SourceScopeCompleted, SourceScopeActive, formatTime(now), SourceScopeUpcoming, now.Year()); err != nil {
		return fmt.Errorf("promote retained upcoming source scopes: %w", err)
	}
	return nil
}

// SourceScopes returns every registered scope in deterministic source order.
func (c *DB) SourceScopes(ctx context.Context) ([]SourceScope, error) {
	return sourceScopes(ctx, c.db)
}

func sourceScopes(ctx context.Context, dbq interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}) ([]SourceScope, error) {
	rows, err := dbq.QueryContext(ctx, `SELECT
		season, stage, registration, lifecycle, discovery, registered_at, updated_at
		FROM source_scopes
		ORDER BY season DESC, stage ASC`)
	if err != nil {
		return nil, fmt.Errorf("query source scopes: %w", err)
	}
	defer func() { _ = rows.Close() }()

	scopes := make([]SourceScope, 0)
	for rows.Next() {
		scope, err := scanSourceScope(rows)
		if err != nil {
			return nil, err
		}
		scopes = append(scopes, scope)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate source scopes: %w", err)
	}
	return scopes, nil
}

// SourceScope returns one registered source identity, if present.
func (c *DB) SourceScope(ctx context.Context, season, stage string) (SourceScope, bool, error) {
	return sourceScope(ctx, c.db, season, stage)
}

func sourceScopeSeeds(configuredSeason, configuredStage string, currentYear int) []sourceScopeSeed {
	byIdentity := make(map[sourceScopeIdentity]sourceScopeSeed)
	add := func(seed sourceScopeSeed) {
		key := sourceScopeIdentity{season: seed.season, stage: seed.stage}
		current, found := byIdentity[key]
		if !found || sourceScopeRegistrationPrecedence(seed.registration) > sourceScopeRegistrationPrecedence(current.registration) {
			byIdentity[key] = seed
		}
	}
	for _, entry := range competition.SourceEntries() {
		add(sourceScopeSeed{season: entry.Season, stage: entry.Stage, registration: SourceScopeCatalog})
	}
	add(sourceScopeSeed{season: configuredSeason, stage: configuredStage, registration: SourceScopeConfigured})
	add(sourceScopeSeed{season: strconv.Itoa(currentYear), stage: "Regular Season", registration: SourceScopeProvisional})
	add(sourceScopeSeed{season: strconv.Itoa(currentYear + 1), stage: "Regular Season", registration: SourceScopeProvisional})

	seeds := make([]sourceScopeSeed, 0, len(byIdentity))
	for _, seed := range byIdentity {
		seeds = append(seeds, seed)
	}
	sort.Slice(seeds, func(i, j int) bool {
		if seeds[i].season != seeds[j].season {
			return seeds[i].season > seeds[j].season
		}
		return seeds[i].stage < seeds[j].stage
	})
	return seeds
}

func sourceScope(ctx context.Context, dbq interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, season, stage string) (SourceScope, bool, error) {
	row := dbq.QueryRowContext(ctx, `SELECT
		season, stage, registration, lifecycle, discovery, registered_at, updated_at
		FROM source_scopes WHERE season = ? AND stage = ?`, season, stage)
	scope, err := scanSourceScope(row)
	if errors.Is(err, sql.ErrNoRows) {
		return SourceScope{}, false, nil
	}
	if err != nil {
		return SourceScope{}, false, fmt.Errorf("load source scope %s %s: %w", season, stage, err)
	}
	return scope, true, nil
}

type sourceScopeScanner interface {
	Scan(...any) error
}

func scanSourceScope(scanner sourceScopeScanner) (SourceScope, error) {
	var scope SourceScope
	var registration, lifecycle, discovery, registeredAt, updatedAt string
	if err := scanner.Scan(&scope.Season, &scope.Stage, &registration, &lifecycle, &discovery, &registeredAt, &updatedAt); err != nil {
		return SourceScope{}, fmt.Errorf("scan source scope: %w", err)
	}
	parsedRegisteredAt, err := time.Parse(time.RFC3339, registeredAt)
	if err != nil {
		return SourceScope{}, fmt.Errorf("parse source scope registered timestamp: %w", err)
	}
	parsedUpdatedAt, err := time.Parse(time.RFC3339, updatedAt)
	if err != nil {
		return SourceScope{}, fmt.Errorf("parse source scope updated timestamp: %w", err)
	}
	scope.Registration = SourceScopeRegistration(registration)
	scope.Lifecycle = SourceScopeLifecycle(lifecycle)
	scope.Discovery = SourceScopeDiscovery(discovery)
	scope.RegisteredAt = parsedRegisteredAt.UTC()
	scope.UpdatedAt = parsedUpdatedAt.UTC()
	return scope, nil
}

func mergeSourceScope(existing SourceScope, incoming sourceScopeSeed, currentYear int) (SourceScope, bool) {
	merged := existing
	if sourceScopeRegistrationPrecedence(incoming.registration) > sourceScopeRegistrationPrecedence(existing.Registration) {
		merged.Registration = incoming.registration
	}
	if sourceScopeSeasonIsPast(existing.Season, currentYear) && existing.Lifecycle != SourceScopeCompleted {
		merged.Lifecycle = SourceScopeCompleted
	} else if existing.Lifecycle == SourceScopeUpcoming && !sourceScopeSeasonIsFuture(existing.Season, currentYear) {
		merged.Lifecycle = SourceScopeActive
	}
	return merged, merged.Registration != existing.Registration || merged.Lifecycle != existing.Lifecycle
}

func seedLifecycle(season string, currentYear int) SourceScopeLifecycle {
	if sourceScopeSeasonIsFuture(season, currentYear) {
		return SourceScopeUpcoming
	}
	if sourceScopeSeasonIsPast(season, currentYear) {
		return SourceScopeCompleted
	}
	return SourceScopeActive
}

func sourceScopeSeasonIsPast(season string, currentYear int) bool {
	if len(season) != 4 {
		return false
	}
	for _, character := range season {
		if character < '0' || character > '9' {
			return false
		}
	}
	year, err := strconv.Atoi(season)
	return err == nil && year < currentYear
}

func sourceScopeSeasonIsFuture(season string, currentYear int) bool {
	if len(season) != 4 {
		return false
	}
	for _, character := range season {
		if character < '0' || character > '9' {
			return false
		}
	}
	year, err := strconv.Atoi(season)
	return err == nil && year > currentYear
}

func sourceScopeRegistrationPrecedence(registration SourceScopeRegistration) int {
	switch registration {
	case SourceScopeCatalog:
		return 4
	case SourceScopeConfigured:
		return 3
	case SourceScopeProvisional:
		return 2
	case SourceScopeObserved:
		return 1
	default:
		return 0
	}
}
