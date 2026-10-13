package apptest

import (
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

// Archived score/identity tuples copied from the local ASA regular-season
// cache on 2026-10-13. Paired with recorded playoff fixtures, this tests
// derivation without expected data, network access or production cache reads.
// Dates and fixture IDs are intentionally synthetic; W/D/L is recorded.
//
//go:embed testdata/threshold-results.json
var thresholdResults []byte

//go:embed testdata/threshold-playoffs.json
var thresholdPlayoffs []byte

func thresholdsArchive(t testing.TB) []cache.HistoricalSeason {
	t.Helper()
	var data struct {
		Teams   [][2]string         `json:"teams"`
		Seasons map[string][][4]int `json:"seasons"`
	}
	if err := json.Unmarshal(thresholdResults, &data); err != nil {
		t.Fatal(err)
	}
	var playoffs map[string][][]json.RawMessage
	if err := json.Unmarshal(thresholdPlayoffs, &playoffs); err != nil {
		t.Fatal(err)
	}
	states := make(map[string]ArchiveState, len(data.Seasons))
	for year := range data.Seasons {
		states[year] = ArchiveState{Lifecycle: cache.SourceScopeCompleted}
	}
	archive := Archive(t, states)
	for i := range archive {
		season := &archive[i]
		season.Data.Games = nil
		season.Data.XGoals = nil
		season.Data.Teams = nil
		for _, tuple := range playoffs[season.Entry.Season] {
			var id, home, away, kickoff string
			for i, target := range []*string{&id, &home, &away, &kickoff} {
				if err := json.Unmarshal(tuple[i], target); err != nil {
					t.Fatal(err)
				}
			}
			var goalsHome, goalsAway int64
			if err := json.Unmarshal(tuple[4], &goalsHome); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(tuple[5], &goalsAway); err != nil {
				t.Fatal(err)
			}
			game := cache.Game{ASAID: id, Season: season.Entry.Season, Stage: "Playoffs", KickoffUTC: kickoff, HomeTeamID: home, AwayTeamID: away, Status: "FullTime", HomeScore: sql.NullInt64{Int64: goalsHome, Valid: true}, AwayScore: sql.NullInt64{Int64: goalsAway, Valid: true}, RawJSON: "{}"}
			for i, target := range []*sql.NullInt64{&game.HomePenalties, &game.AwayPenalties} {
				if string(tuple[6+i]) != "null" {
					if err := json.Unmarshal(tuple[6+i], &target.Int64); err != nil {
						t.Fatal(err)
					}
					target.Valid = true
				}
			}
			game.Penalties = sql.NullBool{Bool: game.HomePenalties.Valid && game.AwayPenalties.Valid, Valid: true}
			season.PlayoffGames = append(season.PlayoffGames, game)
		}
		seen := map[string]bool{}
		for j, tuple := range data.Seasons[season.Entry.Season] {
			home, away := data.Teams[tuple[0]], data.Teams[tuple[1]]
			season.Data.Games = append(season.Data.Games, cache.Game{
				ASAID: fmt.Sprintf("threshold-%s-%d", season.Entry.Season, j), Season: season.Entry.Season, Stage: "Regular Season", Status: "FullTime", KickoffUTC: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(j) * time.Hour).Format("2006-01-02 15:04:05 UTC"),
				HomeTeamID: home[0], AwayTeamID: away[0], HomeScore: sql.NullInt64{Int64: int64(tuple[2]), Valid: true}, AwayScore: sql.NullInt64{Int64: int64(tuple[3]), Valid: true}, RawJSON: "{}",
			})
			for _, team := range [][2]string{home, away} {
				if !seen[team[0]] {
					name := team[1]
					season.Data.Teams = append(season.Data.Teams, standings.Team{ID: team[0], Name: name})
					seen[team[0]] = true
				}
			}
		}
	}
	return archive
}
