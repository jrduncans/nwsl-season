package asatest

import (
	"fmt"
	"math"
	"time"

	"github.com/jrduncans/nwsl-season/internal/asa"
)

// Scenario is a set of fake ASA data built with Season and its methods.
type Scenario struct {
	Teams  []asa.Team
	Games  []asa.Game
	XGoals []asa.GameXGoals
	// Stages maps game_id to stage name; absent games use DefaultStage.
	Stages map[string]string
}

// Season returns a double round robin among teams clubs (team-0 ... team-N),
// every game PreMatch in the base year's season. Round r kicks off at
// base + 7r days; games within a round are staggered one hour apart. An odd
// team count gives each club one bye per leg. It panics if teams < 2.
func Season(teams int, base time.Time) *Scenario {
	if teams < 2 {
		panic("asatest: Season needs at least 2 teams")
	}
	base = base.UTC()
	scenario := &Scenario{Stages: map[string]string{}}
	for i := range teams {
		scenario.Teams = append(scenario.Teams, asa.Team{
			TeamID:           teamID(i),
			TeamName:         fmt.Sprintf("Team %d FC", i),
			TeamShortName:    fmt.Sprintf("Team %d", i),
			TeamAbbreviation: fmt.Sprintf("T%02d", i),
		})
	}

	// Circle method; -1 is the bye slot for odd counts.
	slots := make([]int, 0, teams+1)
	for i := range teams {
		slots = append(slots, i)
	}
	if teams%2 == 1 {
		slots = append(slots, -1)
	}
	n := len(slots)
	legRounds := n - 1
	season := fmt.Sprint(base.Year())
	number := 0
	for round := range 2 * legRounds {
		pairing := round % legRounds
		order := make([]int, n)
		order[0] = slots[0]
		for i := 1; i < n; i++ {
			order[i] = slots[1+(i-1+pairing)%(n-1)]
		}
		slot := 0
		for i := range n / 2 {
			home, away := order[i], order[n-1-i]
			if home < 0 || away < 0 {
				continue
			}
			// Alternate venues within a leg; the second leg swaps them.
			if (i+pairing)%2 == 1 {
				home, away = away, home
			}
			if round >= legRounds {
				home, away = away, home
			}
			kickoff := base.AddDate(0, 0, 7*round).Add(time.Duration(slot) * time.Hour)
			matchday := round + 1
			number++
			scenario.Games = append(scenario.Games, asa.Game{
				GameID:         fmt.Sprintf("game-%03d", number),
				DateTimeUTC:    formatTime(kickoff),
				HomeTeamID:     teamID(home),
				AwayTeamID:     teamID(away),
				StadiumID:      fmt.Sprintf("stadium-%d", home),
				SeasonName:     season,
				Matchday:       &matchday,
				Status:         "PreMatch",
				LastUpdatedUTC: formatTime(base),
			})
			slot++
		}
	}
	return scenario
}

func teamID(i int) string { return fmt.Sprintf("team-%d", i) }

func formatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

// PlayThrough completes every PreMatch game that kicks off before t, using
// score for the final result. It leaves xG alone; call WithXG afterwards.
func (s *Scenario) PlayThrough(t time.Time, score func(game asa.Game) (home, away int)) *Scenario {
	for i, game := range s.Games {
		if game.Status != "PreMatch" {
			continue
		}
		kickoff, err := time.Parse(timeLayout, game.DateTimeUTC)
		if err != nil || !kickoff.Before(t) {
			continue
		}
		home, away := score(game)
		game.HomeScore, game.AwayScore = &home, &away
		game.Status = "FullTime"
		game.LastUpdatedUTC = formatTime(kickoff.Add(3 * time.Hour))
		s.Games[i] = game
	}
	return s
}

// WithXG replaces the scenario's xG with observations for the given share
// (0 to 1) of completed games, spread evenly in game order. Each side's xG is
// its goals plus 0.35 and xPoints follow the xG difference, so results are
// deterministic.
func (s *Scenario) WithXG(fraction float64) *Scenario {
	fraction = math.Max(0, math.Min(1, fraction))
	s.XGoals = nil
	completed := 0
	for _, game := range s.Games {
		if game.Status != "FullTime" || game.HomeScore == nil || game.AwayScore == nil {
			continue
		}
		completed++
		if math.Floor(float64(completed)*fraction) == math.Floor(float64(completed-1)*fraction) {
			continue
		}
		homeXG, awayXG := float64(*game.HomeScore)+0.35, float64(*game.AwayScore)+0.35
		homeXP, awayXP := 1.0, 1.0
		switch {
		case homeXG > awayXG:
			homeXP, awayXP = 2.2, 0.4
		case awayXG > homeXG:
			homeXP, awayXP = 0.4, 2.2
		}
		s.XGoals = append(s.XGoals, asa.GameXGoals{
			GameID:         game.GameID,
			HomeTeamID:     game.HomeTeamID,
			AwayTeamID:     game.AwayTeamID,
			HomeTeamXGoals: homeXG,
			AwayTeamXGoals: awayXG,
			HomeXPoints:    &homeXP,
			AwayXPoints:    &awayXP,
		})
	}
	return s
}
