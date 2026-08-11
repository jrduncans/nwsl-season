package cache

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// UpsertTeams atomically stores one complete team-catalog observation and its
// generalized full-refresh audit. It never deletes omitted teams.
func (c *DB) UpsertTeams(ctx context.Context, teams []Team, metadata FullRefreshMetadata) (SourceRefreshAudit, error) {
	if err := validateTeamCatalog(teams); err != nil {
		return SourceRefreshAudit{}, err
	}
	audit, nextFullDueAt, err := prepareSourceRefresh(SourceRefreshAudit{
		Resource:                SourceResourceTeams,
		Mode:                    SourceRefreshFull,
		Trigger:                 metadata.Trigger,
		StartedAt:               metadata.StartedAt,
		FinishedAt:              metadata.FinishedAt,
		Outcome:                 SourceRefreshSuccess,
		RequestedRows:           0,
		ReturnedRows:            len(teams),
		DownstreamInputsChanged: false,
	}, metadata.NextFullDueAt)
	if err != nil {
		return SourceRefreshAudit{}, err
	}

	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return SourceRefreshAudit{}, fmt.Errorf("begin team catalog refresh: %w", err)
	}
	defer rollback(tx)
	for _, team := range teams {
		change, err := writeTeam(ctx, tx, team, audit.FinishedAt)
		if err != nil {
			return SourceRefreshAudit{}, err
		}
		switch change {
		case rowInserted:
			audit.RowsInserted++
		case rowUpdated:
			audit.RowsUpdated++
		case rowUnchanged:
			audit.RowsUnchanged++
		}
	}
	if err := recordSourceRefresh(ctx, tx, &audit, nextFullDueAt); err != nil {
		return SourceRefreshAudit{}, err
	}
	if err := tx.Commit(); err != nil {
		return SourceRefreshAudit{}, fmt.Errorf("commit team catalog refresh: %w", err)
	}
	return audit, nil
}

func validateTeamCatalog(teams []Team) error {
	if teams == nil {
		return errors.New("team catalog is nil")
	}
	if len(teams) == 0 {
		return errors.New("team catalog is empty")
	}
	seen := make(map[string]struct{}, len(teams))
	for _, team := range teams {
		if strings.TrimSpace(team.ASAID) == "" || team.ASAID != strings.TrimSpace(team.ASAID) {
			return errors.New("team catalog contains a blank or untrimmed team ID")
		}
		if _, ok := seen[team.ASAID]; ok {
			return fmt.Errorf("team catalog contains duplicate team ID %q", team.ASAID)
		}
		seen[team.ASAID] = struct{}{}
	}
	return nil
}
