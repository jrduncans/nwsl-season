package cache

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/jrduncans/nwsl-season/internal/competition"
)

// SourceReadiness describes whether factual fixture inventory has been observed.
type SourceReadiness string

const (
	SourceReadinessUnknown      SourceReadiness = "unknown"
	SourceReadinessNotPublished SourceReadiness = "not_published"
	SourceReadinessAvailable    SourceReadiness = "available"
)

// InventoryCompleteness describes agreement with a verified fixture inventory.
type InventoryCompleteness string

const (
	InventoryCompletenessUnknown    InventoryCompleteness = "unknown"
	InventoryCompletenessIncomplete InventoryCompleteness = "incomplete"
	InventoryCompletenessComplete   InventoryCompleteness = "complete"
)

// SeasonReadinessSnapshot combines one persisted source scope with its factual
// observed inventory and any verified catalog expectation.
type SeasonReadinessSnapshot struct {
	Scope             SourceScope
	Readiness         SourceReadiness
	Completeness      InventoryCompleteness
	ObservedTeams     int
	ObservedGames     int
	ExpectedInventory *competition.InventoryExpectation
}

type observedInventory struct {
	teams       int
	games       int
	appearances map[string]int
}

// SeasonReadiness reads one persisted source scope and its observed fixture
// inventory from one read-only database snapshot.
func (c *DB) SeasonReadiness(ctx context.Context, season, stage string) (SeasonReadinessSnapshot, bool, error) {
	if strings.TrimSpace(season) == "" {
		return SeasonReadinessSnapshot{}, false, errors.New("season readiness season is blank")
	}
	if strings.TrimSpace(stage) == "" {
		return SeasonReadinessSnapshot{}, false, errors.New("season readiness stage is blank")
	}
	tx, err := c.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return SeasonReadinessSnapshot{}, false, fmt.Errorf("begin season readiness read: %w", err)
	}
	defer rollback(tx)
	return seasonReadiness(ctx, tx, season, stage)
}

// SeasonReadinesses reads every persisted source scope and its observed fixture
// inventory from one read-only database snapshot.
func (c *DB) SeasonReadinesses(ctx context.Context) ([]SeasonReadinessSnapshot, error) {
	tx, err := c.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin season readiness list: %w", err)
	}
	defer rollback(tx)

	scopes, err := sourceScopes(ctx, tx)
	if err != nil {
		return nil, err
	}
	snapshots := make([]SeasonReadinessSnapshot, 0, len(scopes))
	for _, scope := range scopes {
		snapshot, found, err := seasonReadinessForScope(ctx, tx, scope)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, fmt.Errorf("source scope %s %s disappeared during readiness read", scope.Season, scope.Stage)
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, nil
}

func seasonReadiness(ctx context.Context, dbq queryer, season, stage string) (SeasonReadinessSnapshot, bool, error) {
	scope, found, err := sourceScope(ctx, dbq, season, stage)
	if err != nil || !found {
		return SeasonReadinessSnapshot{}, found, err
	}
	return seasonReadinessForScope(ctx, dbq, scope)
}

func seasonReadinessForScope(ctx context.Context, dbq queryer, scope SourceScope) (SeasonReadinessSnapshot, bool, error) {
	observed, err := observedInventoryForScope(ctx, dbq, scope.Season, scope.Stage)
	if err != nil {
		return SeasonReadinessSnapshot{}, false, err
	}
	expected := expectedInventoryForScope(scope.Season, scope.Stage)
	readiness, completeness, err := evaluateSeasonReadiness(scope, observed, expected)
	if err != nil {
		return SeasonReadinessSnapshot{}, false, fmt.Errorf("evaluate season readiness %s %s: %w", scope.Season, scope.Stage, err)
	}
	return SeasonReadinessSnapshot{
		Scope:             scope,
		Readiness:         readiness,
		Completeness:      completeness,
		ObservedTeams:     observed.teams,
		ObservedGames:     observed.games,
		ExpectedInventory: copyInventoryExpectation(expected),
	}, true, nil
}

func observedInventoryForScope(ctx context.Context, dbq queryer, season, stage string) (observedInventory, error) {
	rows, err := dbq.QueryContext(ctx, `SELECT home_team_id, away_team_id
		FROM games WHERE season = ? AND stage = ?`, season, stage)
	if err != nil {
		return observedInventory{}, fmt.Errorf("query observed fixture inventory %s %s: %w", season, stage, err)
	}
	defer rows.Close()

	observed := observedInventory{appearances: map[string]int{}}
	for rows.Next() {
		var home, away string
		if err := rows.Scan(&home, &away); err != nil {
			return observedInventory{}, fmt.Errorf("scan observed fixture inventory: %w", err)
		}
		observed.games++
		if home != "" {
			observed.appearances[home]++
		}
		if away != "" {
			observed.appearances[away]++
		}
	}
	if err := rows.Err(); err != nil {
		return observedInventory{}, fmt.Errorf("iterate observed fixture inventory: %w", err)
	}
	observed.teams = len(observed.appearances)
	return observed, nil
}

func expectedInventoryForScope(season, stage string) *competition.InventoryExpectation {
	entry, found := competition.Lookup(season, stage)
	if !found {
		return nil
	}
	return copyInventoryExpectation(entry.Inventory)
}

func copyInventoryExpectation(inventory *competition.InventoryExpectation) *competition.InventoryExpectation {
	if inventory == nil {
		return nil
	}
	copy := *inventory
	return &copy
}

func evaluateSeasonReadiness(scope SourceScope, observed observedInventory, expected *competition.InventoryExpectation) (SourceReadiness, InventoryCompleteness, error) {
	if !validSourceScopeRegistration(scope.Registration) {
		return "", "", fmt.Errorf("source scope registration %q is invalid", scope.Registration)
	}
	if !validSourceScopeLifecycle(scope.Lifecycle) {
		return "", "", fmt.Errorf("source scope lifecycle %q is invalid", scope.Lifecycle)
	}
	if !validSourceScopeDiscovery(scope.Discovery) {
		return "", "", fmt.Errorf("source scope discovery %q is invalid", scope.Discovery)
	}

	readiness := SourceReadinessUnknown
	switch {
	case observed.games > 0 || scope.Discovery == SourceScopeAvailable:
		readiness = SourceReadinessAvailable
	case scope.Discovery == SourceScopeNotPublished:
		readiness = SourceReadinessNotPublished
	}
	if expected == nil {
		return readiness, InventoryCompletenessUnknown, nil
	}

	complete := true
	if expected.Teams != 0 && observed.teams != expected.Teams {
		complete = false
	}
	if expected.Games != 0 && observed.games != expected.Games {
		complete = false
	}
	if expected.GamesPerTeam != 0 {
		if observed.teams != expected.Teams {
			complete = false
		}
		for _, appearances := range observed.appearances {
			if appearances != expected.GamesPerTeam {
				complete = false
			}
		}
	}
	if complete {
		return readiness, InventoryCompletenessComplete, nil
	}
	return readiness, InventoryCompletenessIncomplete, nil
}

func validSourceScopeRegistration(value SourceScopeRegistration) bool {
	switch value {
	case SourceScopeCatalog, SourceScopeConfigured, SourceScopeProvisional, SourceScopeObserved:
		return true
	default:
		return false
	}
}

func validSourceScopeLifecycle(value SourceScopeLifecycle) bool {
	switch value {
	case SourceScopeUpcoming, SourceScopeActive, SourceScopeCompleted:
		return true
	default:
		return false
	}
}

func validSourceScopeDiscovery(value SourceScopeDiscovery) bool {
	switch value {
	case SourceScopeUnknown, SourceScopeNotPublished, SourceScopeAvailable:
		return true
	default:
		return false
	}
}
