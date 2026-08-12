package cache

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"
)

type XGRefreshResult struct {
	Audit     SourceRefreshAudit
	XGRun     *XGSyncRun
	Values    []GameXG
	Freshness []XGFreshness
}

// XGFreshness describes whether a source observation supplied a first value,
// changed an existing value, or only changed source metadata. It is returned
// with the committed refresh so telemetry can distinguish corrections from
// ordinary checks and first observations.
type XGFreshness struct {
	GameID           string
	KickoffUTC       string
	Old              *GameXG
	New              GameXG
	ResponseAccepted bool
	ResponseRejected bool
	RejectionKind    string
	RejectionReason  string
	ValueChanged     bool
	ValueInitialized bool
	MetadataChanged  bool
	Missing          bool
}

func (c *DB) GameXGState(ctx context.Context, id string) (GameXG, bool, error) {
	if invalidTrimmed(id) {
		return GameXG{}, false, errors.New("xG state has blank or untrimmed game ID")
	}
	v, err := loadGameXG(ctx, c.db, id)
	if errors.Is(err, sql.ErrNoRows) {
		return GameXG{}, false, nil
	}
	return v, err == nil, err
}
func (c *DB) GameXGStates(ctx context.Context, season, stage string) ([]GameXG, error) {
	if invalidTrimmed(season) || invalidTrimmed(stage) {
		return nil, errors.New("xG states have blank or untrimmed scope")
	}
	return stageXGStates(ctx, c.db, season, stage)
}
func loadGameXG(ctx context.Context, q queryer, id string) (GameXG, error) {
	var v GameXG
	var first sql.NullString
	var checked string
	err := q.QueryRowContext(ctx, `SELECT availability,home_team_id,away_team_id,home_xg,away_xg,home_xpoints,away_xpoints,raw_json,first_observed_at,last_checked_at FROM game_xg WHERE asa_game_id=?`, id).Scan(&v.Availability, &v.HomeTeamID, &v.AwayTeamID, &v.HomeXG, &v.AwayXG, &v.HomeXPoints, &v.AwayXPoints, &v.RawJSON, &first, &checked)
	if err != nil {
		return GameXG{}, err
	}
	v.GameID = id
	if first.Valid {
		t, e := time.Parse(time.RFC3339, first.String)
		if e != nil {
			return GameXG{}, fmt.Errorf("parse stored xG %q first observation: %w", id, e)
		}
		t = t.UTC()
		v.FirstObservedAt = &t
	}
	t, e := time.Parse(time.RFC3339, checked)
	if e != nil {
		return GameXG{}, fmt.Errorf("parse stored xG %q last check: %w", id, e)
	}
	v.LastCheckedAt = t.UTC()
	if err := validateStoredXG(v); err != nil {
		return GameXG{}, fmt.Errorf("invalid stored xG %q: %w", id, err)
	}
	return v, nil
}

func (c *DB) ReplaceStageXG(ctx context.Context, season, stage string, values []GameXG, metadata FullRefreshMetadata) (XGRefreshResult, error) {
	if invalidTrimmed(season) || invalidTrimmed(stage) || values == nil {
		return XGRefreshResult{}, errors.New("invalid stage xG scope or values")
	}
	seen := map[string]bool{}
	for _, v := range values {
		if err := validateStageXG(v); err != nil {
			return XGRefreshResult{}, err
		}
		if seen[v.GameID] {
			return XGRefreshResult{}, fmt.Errorf("duplicate xG game %q", v.GameID)
		}
		seen[v.GameID] = true
	}
	audit, due, err := prepareSourceRefresh(SourceRefreshAudit{Resource: SourceResourceGameXG, Season: season, Stage: stage, Mode: SourceRefreshFull, Trigger: metadata.Trigger, StartedAt: metadata.StartedAt, FinishedAt: metadata.FinishedAt, Outcome: SourceRefreshSuccess, ReturnedRows: len(values)}, metadata.NextFullDueAt)
	if err != nil {
		return XGRefreshResult{}, err
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return XGRefreshResult{}, err
	}
	defer rollback(tx)
	games, err := seasonGames(ctx, tx, season, stage)
	if err != nil {
		return XGRefreshResult{}, err
	}
	candidate := map[string]Game{}
	for _, g := range games {
		if gameFullTime(g) {
			candidate[g.ASAID] = g
		}
	}
	badIDs := []string{}
	for _, v := range values {
		g, ok := candidate[v.GameID]
		if !ok || g.HomeTeamID != v.HomeTeamID || g.AwayTeamID != v.AwayTeamID {
			badIDs = append(badIDs, v.GameID)
		}
	}
	if len(badIDs) > 0 {
		sort.Strings(badIDs)
		return XGRefreshResult{}, fmt.Errorf("invalid xG game identities %v", badIDs)
	}
	ids := make([]string, 0, len(candidate))
	for id := range candidate {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	returned := map[string]GameXG{}
	for _, v := range values {
		returned[v.GameID] = v
	}
	material := false
	freshness := make([]XGFreshness, 0, len(ids))
	for _, id := range ids {
		g := candidate[id]
		v, ok := returned[id]
		returnedAvailable := ok && v.Availability == XGAvailable
		if !ok {
			v = GameXG{GameID: id, Availability: XGUnavailable, HomeTeamID: g.HomeTeamID, AwayTeamID: g.AwayTeamID}
		}
		telemetryValue := v
		telemetryValue.LastCheckedAt = audit.FinishedAt
		old, oldErr := loadGameXG(ctx, tx, id)
		oldFound := oldErr == nil
		if oldErr != nil && !errors.Is(oldErr, sql.ErrNoRows) {
			return XGRefreshResult{}, fmt.Errorf("load previous xG %q: %w", id, oldErr)
		}
		change, mat, accepted, err := writeStageXG(ctx, tx, v, audit.FinishedAt)
		if err != nil {
			return XGRefreshResult{}, err
		}
		freshness = append(freshness, xGFreshness(id, g.KickoffUTC, old, oldFound, telemetryValue, returnedAvailable, accepted))
		material = material || mat
		switch change {
		case rowInserted:
			audit.RowsInserted++
		case rowUpdated:
			audit.RowsUpdated++
		default:
			audit.RowsUnchanged++
		}
		if err := upsertGameXGCheck(ctx, tx, id, audit.FinishedAt, nil, true, returnedAvailable, mat); err != nil {
			return XGRefreshResult{}, err
		}
	}
	out, err := stageXGStates(ctx, tx, season, stage)
	if err != nil {
		return XGRefreshResult{}, err
	}
	before, err := venueXG(ctx, tx, season, stage)
	if err != nil {
		return XGRefreshResult{}, err
	}
	if err := updateVenueXGSummary(ctx, tx, season, stage, audit.FinishedAt); err != nil {
		return XGRefreshResult{}, err
	}
	after, err := venueXG(ctx, tx, season, stage)
	if err != nil {
		return XGRefreshResult{}, err
	}
	audit.DownstreamInputsChanged = material || before != after
	run := &XGSyncRun{StartedAt: audit.StartedAt, FinishedAt: audit.FinishedAt, Season: season, Stage: stage, Outcome: "success", RowsSeen: int64(len(values)), RowsInserted: int64(audit.RowsInserted), RowsUpdated: int64(audit.RowsUpdated), RowsUnchanged: int64(audit.RowsUnchanged)}
	for _, id := range ids {
		v, err := loadGameXG(ctx, tx, id)
		if err != nil {
			return XGRefreshResult{}, fmt.Errorf("load committed xG %q for legacy lineage: %w", id, err)
		}
		if v.Availability == XGAvailable {
			run.AvailableGames++
		} else {
			run.UnavailableGames++
		}
	}
	if err := insertXGSyncRun(ctx, tx, run); err != nil {
		return XGRefreshResult{}, err
	}
	if err := recordSourceRefresh(ctx, tx, &audit, due); err != nil {
		return XGRefreshResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return XGRefreshResult{}, err
	}
	return XGRefreshResult{Audit: audit, XGRun: run, Values: append([]GameXG{}, out...), Freshness: freshness}, nil
}

const xGStaleRejectionReason = "observation_not_newer_than_cached_check"

func xGFreshness(id, kickoffUTC string, old GameXG, oldFound bool, incoming GameXG, returnedAvailable, accepted bool) XGFreshness {
	freshness := XGFreshness{GameID: id, KickoffUTC: kickoffUTC, New: incoming, ResponseAccepted: accepted, ResponseRejected: !accepted}
	if oldFound {
		oldCopy := old
		freshness.Old = &oldCopy
	}
	if !accepted {
		freshness.RejectionKind = "stale"
		freshness.RejectionReason = xGStaleRejectionReason
		return freshness
	}
	if !returnedAvailable {
		freshness.Missing = true
		return freshness
	}
	if !oldFound || old.Availability != XGAvailable {
		freshness.ValueInitialized = true
		return freshness
	}
	if availableXGMaterialChange(old, incoming) {
		freshness.ValueChanged = true
		return freshness
	}
	if !sameAvailableXG(old, incoming) {
		freshness.MetadataChanged = true
	}
	return freshness
}
func validateStageXG(v GameXG) error {
	if invalidTrimmed(v.GameID) || invalidTrimmed(v.HomeTeamID) || invalidTrimmed(v.AwayTeamID) || v.HomeTeamID == v.AwayTeamID || v.Availability != XGAvailable || !v.HomeXG.Valid || !v.AwayXG.Valid || !finiteNonnegative(v.HomeXG.Float64) || !finiteNonnegative(v.AwayXG.Float64) || v.HomeXPoints.Valid != v.AwayXPoints.Valid || (v.HomeXPoints.Valid && (!validGameExpectedPoints(v.HomeXPoints.Float64) || !validGameExpectedPoints(v.AwayXPoints.Float64))) || v.FirstObservedAt != nil || !v.LastCheckedAt.IsZero() {
		return errors.New("invalid stage xG value")
	}
	return nil
}
func writeStageXG(ctx context.Context, tx *sql.Tx, v GameXG, at time.Time) (rowChange, bool, bool, error) {
	if v.Availability == XGAvailable {
		return writeAvailableStageXG(ctx, tx, v, at)
	}
	_, err := loadGameXG(ctx, tx, v.GameID)
	if errors.Is(err, sql.ErrNoRows) {
		ch, e := writeGameXG(ctx, tx, v, at)
		return ch, v.Availability == XGAvailable, true, e
	}
	if err != nil {
		return 0, false, false, err
	}
	_, e := tx.ExecContext(ctx, `UPDATE game_xg SET last_checked_at=? WHERE asa_game_id=? AND last_checked_at<?`, formatTime(at), v.GameID, formatTime(at))
	return rowUnchanged, false, true, e
}

func writeAvailableStageXG(ctx context.Context, tx *sql.Tx, v GameXG, at time.Time) (rowChange, bool, bool, error) {
	old, err := loadGameXG(ctx, tx, v.GameID)
	if errors.Is(err, sql.ErrNoRows) {
		change, err := writeGameXG(ctx, tx, v, at)
		return change, true, true, err
	}
	if err != nil {
		return 0, false, false, err
	}
	if old.Availability == XGAvailable {
		identical := sameAvailableXG(old, v)
		if !identical && !at.After(old.LastCheckedAt) {
			return rowUnchanged, false, false, nil
		}
		if identical && !at.After(old.LastCheckedAt) {
			return rowUnchanged, false, false, nil
		}
		change, err := writeGameXG(ctx, tx, v, at)
		return change, availableXGMaterialChange(old, v), true, err
	}
	material := true
	change, err := writeGameXG(ctx, tx, v, at)
	if err == nil && !at.After(old.LastCheckedAt) {
		_, err = tx.ExecContext(ctx, `UPDATE game_xg SET last_checked_at=? WHERE asa_game_id=?`, formatTime(old.LastCheckedAt), v.GameID)
	}
	return change, material, true, err
}

func sameAvailableXG(old, incoming GameXG) bool {
	return old.Availability == incoming.Availability && old.HomeTeamID == incoming.HomeTeamID && old.AwayTeamID == incoming.AwayTeamID && old.HomeXG == incoming.HomeXG && old.AwayXG == incoming.AwayXG && old.HomeXPoints == incoming.HomeXPoints && old.AwayXPoints == incoming.AwayXPoints && old.RawJSON == incoming.RawJSON
}

func availableXGMaterialChange(old, incoming GameXG) bool {
	return old.Availability != incoming.Availability || old.HomeXG != incoming.HomeXG || old.AwayXG != incoming.AwayXG || old.HomeXPoints != incoming.HomeXPoints || old.AwayXPoints != incoming.AwayXPoints
}

func stageXGStates(ctx context.Context, q queryer, season, stage string) ([]GameXG, error) {
	rows, err := q.QueryContext(ctx, `SELECT x.asa_game_id,x.availability,x.home_team_id,x.away_team_id,x.home_xg,x.away_xg,x.home_xpoints,x.away_xpoints,x.raw_json,x.first_observed_at,x.last_checked_at FROM game_xg x JOIN games g ON g.asa_game_id=x.asa_game_id WHERE g.season=? AND g.stage=? ORDER BY x.asa_game_id`, season, stage)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GameXG{}
	for rows.Next() {
		var v GameXG
		var first sql.NullString
		var checked string
		if err := rows.Scan(&v.GameID, &v.Availability, &v.HomeTeamID, &v.AwayTeamID, &v.HomeXG, &v.AwayXG, &v.HomeXPoints, &v.AwayXPoints, &v.RawJSON, &first, &checked); err != nil {
			return nil, err
		}
		if first.Valid {
			t, e := time.Parse(time.RFC3339, first.String)
			if e != nil {
				return nil, fmt.Errorf("parse stored xG %q first observation: %w", v.GameID, e)
			}
			t = t.UTC()
			v.FirstObservedAt = &t
		}
		t, e := time.Parse(time.RFC3339, checked)
		if e != nil {
			return nil, fmt.Errorf("parse stored xG %q last check: %w", v.GameID, e)
		}
		v.LastCheckedAt = t.UTC()
		if err := validateStoredXG(v); err != nil {
			return nil, fmt.Errorf("invalid stored xG %q: %w", v.GameID, err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func validateStoredXG(v GameXG) error {
	if invalidTrimmed(v.GameID) {
		return errors.New("game identity")
	}
	if v.Availability != XGAvailable && v.Availability != XGUnavailable {
		return errors.New("availability")
	}
	if invalidTrimmed(v.HomeTeamID) || invalidTrimmed(v.AwayTeamID) || v.HomeTeamID == v.AwayTeamID {
		return errors.New("team identity")
	}
	if v.LastCheckedAt.IsZero() {
		return errors.New("last check")
	}
	if v.HomeXPoints.Valid != v.AwayXPoints.Valid || (v.HomeXPoints.Valid && (!validGameExpectedPoints(v.HomeXPoints.Float64) || !validGameExpectedPoints(v.AwayXPoints.Float64))) {
		return errors.New("expected points")
	}
	if v.Availability == XGAvailable && (!v.HomeXG.Valid || !v.AwayXG.Valid || !finiteNonnegative(v.HomeXG.Float64) || !finiteNonnegative(v.AwayXG.Float64) || v.FirstObservedAt == nil) {
		return errors.New("available values")
	}
	if v.Availability == XGUnavailable && (v.HomeXG.Valid || v.AwayXG.Valid || v.HomeXPoints.Valid || v.AwayXPoints.Valid || v.FirstObservedAt != nil || v.RawJSON != "") {
		return errors.New("unavailable values")
	}
	return nil
}
func venueXG(ctx context.Context, q queryer, season, stage string) ([4]float64, error) {
	var ready int
	var n int
	var h, a float64
	err := q.QueryRowContext(ctx, `SELECT xg_ready,xg_matches,home_xg,away_xg FROM venue_summaries WHERE season=? AND stage=?`, season, stage).Scan(&ready, &n, &h, &a)
	if errors.Is(err, sql.ErrNoRows) {
		return [4]float64{}, nil
	}
	return [4]float64{float64(ready), float64(n), h, a}, err
}
