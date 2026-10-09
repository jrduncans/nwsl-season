package apptest

import (
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

// Scenario names accepted by Scenario, Seed and cmd/preview.
const (
	ScenarioDefault     = "default"
	ScenarioTeams       = "teams"
	ScenarioTeamHistory = "team-history"
	ScenarioNoXG        = "no-xg"
	ScenarioSeasonTrend = "season-trend"
	ScenarioOverview    = "overview"
	ScenarioEmpty       = "empty"
	ScenarioSingle      = "single"
)

// ScenarioNames lists every scenario in the order cmd/preview documents them.
func ScenarioNames() []string {
	return []string{ScenarioDefault, ScenarioTeams, ScenarioNoXG, ScenarioTeamHistory, ScenarioSeasonTrend, ScenarioOverview, ScenarioEmpty, ScenarioSingle}
}

// Scenario returns the in-memory historical seasons for a named scenario and
// the season that the history scoring page should preselect. An empty name
// means ScenarioDefault. An unknown name fails the test.
func Scenario(t testing.TB, name string) (archive []cache.HistoricalSeason, selection string) {
	t.Helper()
	selection = "2022"
	switch name {
	case "", ScenarioDefault:
		archive = Archive(t, map[string]ArchiveState{
			"2018": {Lifecycle: cache.SourceScopeCompleted, Inventory: cache.InventoryCompletenessComplete, Goals: 2, XGCovered: 20},
			"2019": {Lifecycle: cache.SourceScopeCompleted, Goals: 3, XGCovered: 19},
			"2021": {Lifecycle: cache.SourceScopeActive, Goals: 2, XGCovered: 20},
			"2022": {Lifecycle: cache.SourceScopeCompleted, Inventory: cache.InventoryCompletenessIncomplete, Goals: 1},
		})
	case ScenarioTeams, ScenarioNoXG, ScenarioTeamHistory, ScenarioSeasonTrend:
		archive = teamsArchive(t, name)
	case ScenarioOverview:
		states := make(map[string]ArchiveState)
		for _, year := range []string{"2016", "2017", "2018", "2019", "2021", "2022", "2023", "2024", "2025", "2026"} {
			states[year] = ArchiveState{Lifecycle: cache.SourceScopeCompleted, Goals: 3, XGCovered: 20}
		}
		states["2026"] = ArchiveState{Lifecycle: cache.SourceScopeActive, Inventory: cache.InventoryCompletenessComplete, Goals: 3, XGCovered: 20}
		archive = Archive(t, states)
		for i := range archive {
			for j := range archive[i].Data.Games {
				game := &archive[i].Data.Games[j]
				// Include all five bins while varying each season deterministically.
				total := int64((j + int(archive[i].Entry.Season[len(archive[i].Entry.Season)-1])) % 6)
				game.HomeScore.Int64, game.AwayScore.Int64 = total/2, total-total/2
				archive[i].Data.XGoals[j].HomeXG.Float64 = 1.2 + float64(j%3)*0.1
				archive[i].Data.XGoals[j].AwayXG.Float64 = 1.1
			}
		}
		selection = "2025"
	case ScenarioEmpty:
		archive = Archive(t, map[string]ArchiveState{"2024": {Lifecycle: cache.SourceScopeActive, Goals: 1}})
		archive[0].Data.Games = Games("2024", 19, 1)
		selection = "2024"
	case ScenarioSingle:
		archive = Archive(t, map[string]ArchiveState{"2024": {Lifecycle: cache.SourceScopeCompleted, Goals: 1}})
		selection = "2024"
	default:
		t.Fatalf("unknown scenario %q; want one of %v", name, ScenarioNames())
	}
	return archive, selection
}

// previewTeams are the sixteen 2026 clubs used by the teams, team-history and
// season-trend scenarios.
func previewTeams() []standings.Team {
	return []standings.Team{
		{ID: "kRQa8JOqKZ", Name: "Angel City FC"}, {ID: "315VnJ759x", Name: "Bay FC"},
		{ID: "odMX2OJqYL", Name: "Boston Legacy FC"}, {ID: "KPqjw8PQ6v", Name: "Chicago Stars FC"},
		{ID: "2lqRn34qr0", Name: "Denver Summit FC"}, {ID: "raMyrr25d2", Name: "Gotham FC"},
		{ID: "4JMAk47qKg", Name: "Houston Dash"}, {ID: "4wM4rZdqjB", Name: "Kansas City Current"},
		{ID: "zeQZeazqKw", Name: "North Carolina Courage"}, {ID: "XVqKeVKM01", Name: "Orlando Pride"},
		{ID: "Pk5LeeNqOW", Name: "Portland Thorns FC"}, {ID: "eV5DR6YQKn", Name: "Racing Louisville FC"},
		{ID: "7VqG1lYMvW", Name: "San Diego Wave FC"}, {ID: "7vQ7BBzqD1", Name: "Seattle Reign FC"},
		{ID: "eV5D2w9QKn", Name: "Utah Royals FC"}, {ID: "aDQ0lzvQEv", Name: "Washington Spirit"},
	}
}

var previewStadiums = []string{"7vQ7xbOMD1", "Vj58W84M8n", "2lqRXGLMr0", "KXMe8lXQ64", "p6qb18650G", "NWMW84L5lz", "0x5g6ojM7O", "xW5p3L0Mg1", "gpMOrLOQzy", "vzqoJrj5ap", "p6qbX06M0G", "BLMvra8Mxe", "Oa5wKXY514", "9Yqda07QvJ", "e7MzlRjqr0", "xW5pwORMg1"}

func teamsArchive(t testing.TB, scenario string) []cache.HistoricalSeason {
	t.Helper()
	archive := Archive(t, map[string]ArchiveState{
		"2024": {Lifecycle: cache.SourceScopeUpcoming},
		"2025": {Lifecycle: cache.SourceScopeCompleted, Goals: 3},
		"2026": {Lifecycle: cache.SourceScopeActive, Goals: 3},
	})
	if scenario == ScenarioTeamHistory {
		for _, year := range []string{"2016", "2017", "2018", "2019", "2021", "2022", "2023"} {
			archive = append(archive, Archive(t, map[string]ArchiveState{
				year: {Lifecycle: cache.SourceScopeCompleted, Goals: 3},
			})...)
		}
	}
	for i := range archive {
		season := &archive[i]
		if season.Entry.Season == "2024" {
			season.Data.Games = nil
			continue
		}
		season.Data.Teams = previewTeams()
		count := 64
		if scenario == ScenarioSeasonTrend || scenario == ScenarioNoXG {
			count = 240
		}
		season.Data.Games = Games(season.Entry.Season, count, 3)
		for j := range season.Data.Games {
			game := &season.Data.Games[j]
			game.RawJSON = fmt.Sprintf(`{"stadium_id":%q}`, previewStadiums[j%16])
			game.KickoffUTC = time.Date(2026, 3, 2, 1, 0, 0, 0, time.UTC).AddDate(0, 0, j/8*7).Format("2006-01-02 15:04:05 MST")
			game.HomeTeamID, game.AwayTeamID = season.Data.Teams[j%16].ID, season.Data.Teams[(j+5)%16].ID
			if scenario == ScenarioSeasonTrend || scenario == ScenarioNoXG {
				// Two round-robin legs keep every team at one fixture per
				// kickoff and one remaining fixture in the active season.
				round, pairing := j/8, j%8
				home, away := 15, round%15
				if pairing > 0 {
					home, away = (round+pairing)%15, (round+15-pairing)%15
				}
				if round >= 15 {
					home, away = away, home
				}
				game.HomeTeamID, game.AwayTeamID = season.Data.Teams[home].ID, season.Data.Teams[away].ID
			}
			game.HomeScore.Int64, game.AwayScore.Int64 = int64(j%4), int64((j/3)%3)
			if scenario == ScenarioTeamHistory {
				year := int(season.Entry.Season[3] - '0')
				game.HomeScore.Int64, game.AwayScore.Int64 = int64((j+year)%5), int64((j/3+year)%3)
			}
			if (scenario == ScenarioSeasonTrend || scenario == ScenarioNoXG) && season.Entry.Season == "2026" && j >= count-8 {
				game.Status = "PreMatch"
				game.HomeScore, game.AwayScore = sql.NullInt64{}, sql.NullInt64{}
				continue
			}
			if season.Entry.Season == "2022" && j == 1 {
				continue // One completed game without xG.
			}
			observation := cache.GameXG{
				GameID: game.ASAID, Availability: cache.XGAvailable, HomeTeamID: game.HomeTeamID, AwayTeamID: game.AwayTeamID,
				HomeXG: sql.NullFloat64{Float64: .5 + float64(j%7)*.3, Valid: true}, AwayXG: sql.NullFloat64{Float64: .2 + float64(j%5)*.25, Valid: true},
				HomeXPoints: sql.NullFloat64{Float64: 1.3 + float64(j%5)*.12, Valid: true},
				AwayXPoints: sql.NullFloat64{Float64: 1.3 - float64(j%5)*.08, Valid: true},
			}
			if scenario == ScenarioNoXG && j > 0 {
				// Only the first game has xG, so every team misses some and
				// none has complete xG while the season still has xG data.
				observation.HomeXG.Valid = false
			}
			if season.Entry.Season == "2026" && j == 1 {
				observation.HomeXPoints.Valid = false // xG covered, xPts missing.
			}
			if season.Entry.Season == "2026" && j == 2 {
				observation.HomeXG.Valid = false // xPts covered, xG missing.
			}
			season.Data.XGoals = append(season.Data.XGoals, observation)
		}
	}
	return archive
}
