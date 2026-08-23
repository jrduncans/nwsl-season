// Package scheduleload calculates the recovery and accumulated-load context
// shared by forecast and descriptive schedule-strength calculations.
package scheduleload

import (
	"fmt"
	"sort"
	"time"

	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

const (
	RecoveryStart = 6 * 24 * time.Hour
	RecoveryFull  = 5 * 24 * time.Hour
	LoadWindow    = 9 * 24 * time.Hour

	// Development seasons supported effects around 0.16 and 0.30 on the
	// relative log-strength scale. The frozen candidate keeps one quarter of
	// each exploratory estimate because development match results were noisy
	// and heterogeneous by season.
	RecoveryLogShift        = 0.04
	AccumulatedLoadLogShift = 0.075
)

// Team is one team's load entering a fixture.
type Team struct {
	Recovery        float64
	ThirdWithinNine bool
}

// Fixture contains the home and away teams' load entering one fixture.
type Fixture struct {
	Home Team
	Away Team
}

type appearance struct {
	gameID  string
	kickoff time.Time
	home    bool
}

// Calculate derives load entering every completed or scheduled fixture.
func Calculate(games []standings.Game) (map[string]Fixture, error) {
	byTeam := map[string][]appearance{}
	seen := make(map[string]struct{}, len(games))
	for _, game := range games {
		if _, exists := seen[game.ID]; exists {
			return nil, fmt.Errorf("duplicate fixture %q in schedule-load context", game.ID)
		}
		seen[game.ID] = struct{}{}
		if game.Status != standings.CompletedStatus && game.Status != fixtures.PreMatchStatus {
			continue
		}
		if game.Kickoff.IsZero() {
			return nil, fmt.Errorf("fixture %q has no kickoff for schedule-load context", game.ID)
		}
		byTeam[game.HomeTeamID] = append(byTeam[game.HomeTeamID], appearance{gameID: game.ID, kickoff: game.Kickoff, home: true})
		byTeam[game.AwayTeamID] = append(byTeam[game.AwayTeamID], appearance{gameID: game.ID, kickoff: game.Kickoff})
	}
	loads := make(map[string]Fixture, len(games))
	for teamID, appearances := range byTeam {
		sort.Slice(appearances, func(i, j int) bool {
			if appearances[i].kickoff.Equal(appearances[j].kickoff) {
				return appearances[i].gameID < appearances[j].gameID
			}
			return appearances[i].kickoff.Before(appearances[j].kickoff)
		})
		for index := 1; index < len(appearances); index++ {
			if appearances[index].kickoff.Equal(appearances[index-1].kickoff) {
				return nil, fmt.Errorf("team %q has multiple fixtures at %s", teamID, appearances[index].kickoff.UTC().Format(time.RFC3339Nano))
			}
		}
		for index, appearance := range appearances {
			var load Team
			if index > 0 {
				load.Recovery = RecoveryPressure(appearance.kickoff.Sub(appearances[index-1].kickoff))
			}
			if index > 1 {
				span := appearance.kickoff.Sub(appearances[index-2].kickoff)
				load.ThirdWithinNine = span >= 0 && span <= LoadWindow
			}
			fixture := loads[appearance.gameID]
			if appearance.home {
				fixture.Home = load
			} else {
				fixture.Away = load
			}
			loads[appearance.gameID] = fixture
		}
	}
	return loads, nil
}

// Congestion converts recovery and accumulated load into the frozen model's
// relative log-strength penalty.
func Congestion(load Team) float64 {
	value := RecoveryLogShift * load.Recovery
	if load.ThirdWithinNine {
		value += AccumulatedLoadLogShift
	}
	return value
}

// RecoveryPressure is zero at six elapsed days, rises linearly to one at five
// days, and remains one for shorter recovery.
func RecoveryPressure(rest time.Duration) float64 {
	if rest >= RecoveryStart {
		return 0
	}
	if rest <= RecoveryFull {
		return 1
	}
	return float64(RecoveryStart-rest) / float64(RecoveryStart-RecoveryFull)
}
