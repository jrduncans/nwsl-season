package forecast

import (
	"encoding/binary"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

const (
	xgPoissonScheduleLoadID = "xg-poisson-schedule-load-v1"

	recoveryStart = 6 * 24 * time.Hour
	recoveryFull  = 5 * 24 * time.Hour
	loadWindow    = 9 * 24 * time.Hour

	// Development seasons supported effects around 0.16 and 0.30 on the
	// relative log-strength scale. The frozen candidate keeps one quarter of
	// each exploratory estimate because development match results were noisy
	// and heterogeneous by season.
	recoveryLogShift        = 0.04
	accumulatedLoadLogShift = 0.075
)

// NewXGPoissonScheduleLoadV1 adjusts the established xG Poisson rates for a
// team's recovery time and for playing a third match inside nine days.
func NewXGPoissonScheduleLoadV1() Model {
	return xgPoissonScheduleLoadV1{}
}

type xgPoissonScheduleLoadV1 struct{}

func (p xgPoissonScheduleLoadV1) Info() Info {
	return Info{
		ID:          xgPoissonScheduleLoadID,
		Name:        "xG Poisson (schedule load)",
		Description: "An experimental xG Poisson model that adjusts each team's scoring and defending rates for short recovery and a third match within nine days.",
		Inputs:      "Available ASA team-model xG, current-season fixture kickoffs, and the two previous regular seasons for league home and away xG rates.",
		Assumptions: "Recovery pressure increases between six and five elapsed days and then saturates; a third match within nine elapsed days adds a separate accumulated-load effect. Development-only estimates are strongly shrunk and adjust the teams' relative strength without changing their geometric-mean scoring rate.",
	}
}

type teamScheduleLoad struct {
	recovery float64
	third    bool
}

type fixtureScheduleLoad struct {
	home, away teamScheduleLoad
}

type scheduleLoadXGPredictor struct {
	base     xgPredictor
	fixtures map[string]fixtureScheduleLoad
	material []byte
}

func (p xgPoissonScheduleLoadV1) Fit(input FitInput) (Predictor, error) {
	base, err := fitXGPoissonHomeTwoSeasons(input)
	if err != nil {
		return nil, err
	}
	venue, ok := base.(xgPredictor)
	if !ok {
		return nil, fmt.Errorf("schedule-load base predictor has type %T", base)
	}
	loads, err := scheduleLoads(input.Games)
	if err != nil {
		return nil, err
	}
	return scheduleLoadXGPredictor{
		base: venue, fixtures: loads,
		material: appendScheduleLoadMaterial(venue.material, input.Games),
	}, nil
}

func (p scheduleLoadXGPredictor) SeedMaterial() []byte {
	return append([]byte(nil), p.material...)
}

func (p scheduleLoadXGPredictor) Distribution(game standings.Game) (Distribution, error) {
	homeRate, awayRate, err := p.base.rates(game)
	if err != nil {
		return nil, err
	}
	load, ok := p.fixtures[game.ID]
	if !ok {
		return nil, fmt.Errorf("fixture %q is absent from schedule-load context", game.ID)
	}
	homeCongestion := congestion(load.home)
	awayCongestion := congestion(load.away)
	adjustment := (awayCongestion - homeCongestion) / 2
	return poissonDistribution{
		homeRate: clamp(homeRate * math.Exp(adjustment)),
		awayRate: clamp(awayRate * math.Exp(-adjustment)),
	}, nil
}

func congestion(load teamScheduleLoad) float64 {
	value := recoveryLogShift * load.recovery
	if load.third {
		value += accumulatedLoadLogShift
	}
	return value
}

type scheduledAppearance struct {
	gameID  string
	kickoff time.Time
	home    bool
}

func scheduleLoads(games []standings.Game) (map[string]fixtureScheduleLoad, error) {
	byTeam := map[string][]scheduledAppearance{}
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
		byTeam[game.HomeTeamID] = append(byTeam[game.HomeTeamID], scheduledAppearance{gameID: game.ID, kickoff: game.Kickoff, home: true})
		byTeam[game.AwayTeamID] = append(byTeam[game.AwayTeamID], scheduledAppearance{gameID: game.ID, kickoff: game.Kickoff})
	}
	loads := make(map[string]fixtureScheduleLoad, len(games))
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
			var load teamScheduleLoad
			if index > 0 {
				load.recovery = recoveryPressure(appearance.kickoff.Sub(appearances[index-1].kickoff))
			}
			if index > 1 {
				span := appearance.kickoff.Sub(appearances[index-2].kickoff)
				load.third = span >= 0 && span <= loadWindow
			}
			fixture := loads[appearance.gameID]
			if appearance.home {
				fixture.home = load
			} else {
				fixture.away = load
			}
			loads[appearance.gameID] = fixture
		}
	}
	return loads, nil
}

func recoveryPressure(rest time.Duration) float64 {
	if rest >= recoveryStart {
		return 0
	}
	if rest <= recoveryFull {
		return 1
	}
	return float64(recoveryStart-rest) / float64(recoveryStart-recoveryFull)
}

func appendScheduleLoadMaterial(base []byte, games []standings.Game) []byte {
	material := append([]byte(nil), base...)
	material = append(material, "schedule-load-xg-v1"...)
	ordered := append([]standings.Game(nil), games...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	var value [8]byte
	for _, game := range ordered {
		if game.Status != standings.CompletedStatus && game.Status != fixtures.PreMatchStatus {
			continue
		}
		for _, text := range []string{game.ID, game.Status, game.HomeTeamID, game.AwayTeamID} {
			binary.BigEndian.PutUint64(value[:], uint64(len(text)))
			material = append(material, value[:]...)
			material = append(material, text...)
		}
		binary.BigEndian.PutUint64(value[:], uint64(game.Kickoff.UnixNano()))
		material = append(material, value[:]...)
	}
	return material
}
