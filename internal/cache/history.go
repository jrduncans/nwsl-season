package cache

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strconv"

	"github.com/jrduncans/nwsl-season/internal/competition"
)

// HistoricalSeason is one public, source-backed regular season and the cached
// data observed for it. Readiness is nil when that catalog scope has not been
// persisted yet.
type HistoricalSeason struct {
	Entry     competition.Entry
	Readiness *SeasonReadinessSnapshot
	Data      SeasonData
}

// HistoricalRegularSeasons reads every public, source-backed, fixture-capable
// Regular Season catalog entry from one cache snapshot. It never creates
// source scopes or refreshes upstream data.
func (c *DB) HistoricalRegularSeasons(ctx context.Context) ([]HistoricalSeason, error) {
	entries := historicalRegularSeasonEntries()
	tx, err := c.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin historical regular seasons read: %w", err)
	}
	defer rollback(tx)

	seasons := make([]HistoricalSeason, 0, len(entries))
	for _, entry := range entries {
		readiness, found, err := seasonReadiness(ctx, tx, entry.Season, entry.Stage)
		if err != nil {
			return nil, fmt.Errorf("load historical readiness %s %s: %w", entry.Season, entry.Stage, err)
		}
		data, err := loadSeasonData(ctx, tx, entry.Season, entry.Stage)
		if err != nil {
			return nil, fmt.Errorf("load historical season %s %s: %w", entry.Season, entry.Stage, err)
		}
		season := HistoricalSeason{Entry: entry, Data: data}
		if found {
			readinessCopy := readiness
			season.Readiness = &readinessCopy
		}
		seasons = append(seasons, season)
	}
	return seasons, nil
}

func historicalRegularSeasonEntries() []competition.Entry {
	entries := make([]competition.Entry, 0)
	for _, entry := range competition.PublicEntries() {
		if entry.Stage == "Regular Season" && entry.SourceAvailable && entry.Supports(competition.CapabilityFixtures) {
			entries = append(entries, entry)
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		left, _ := strconv.Atoi(entries[i].Season)
		right, _ := strconv.Atoi(entries[j].Season)
		return left < right
	})
	return entries
}
