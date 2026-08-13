package forecast

import (
	"encoding/binary"
	"fmt"
	"math"
	"sort"

	"github.com/jrduncans/nwsl-season/internal/standings"
)

const xgPoissonID = "xg-poisson-v1"

func NewXGPoissonV1() Model { return xgPoissonV1{} }

type xgPoissonV1 struct{}

func (xgPoissonV1) Info() Info {
	return Info{ID: xgPoissonID, Name: "xG Poisson", Description: "Simulates remaining games from each team's xG averages so far, adjusted for home-field advantage.", Inputs: "Available ASA xG for completed matches and the remaining schedule.", Assumptions: "Home-field advantage reflects the league's average xG difference between home and away teams this season. Early results are balanced with league-wide xG; matches without xG are left out."}
}

type xgTotals struct {
	played            int
	forGoals, against float64
}
type xgPredictor struct {
	teams                          map[string]xgTotals
	homeRate, awayRate, leagueRate float64
	material                       []byte
}

func (xgPoissonV1) Fit(input FitInput) (Predictor, error) {
	return fitXGPoisson(input, input.Games, input.XGoals, VenueSample{})
}

// fitXGPoisson keeps current-season team xG strengths separate from the
// league-wide home and away xG sample. This permits evaluation candidates to
// test a larger venue sample without carrying former squads into current-team
// strengths.
func fitXGPoisson(input FitInput, leagueGames []standings.Game, leagueXG map[string]ExpectedGoals, historical VenueSample) (Predictor, error) {
	known, err := validateTeams(input.Teams)
	if err != nil {
		return nil, err
	}
	totals := map[string]xgTotals{}
	for id := range known {
		totals[id] = xgTotals{}
	}
	for _, game := range input.Games {
		if game.Status != standings.CompletedStatus {
			continue
		}
		xg, ok := input.XGoals[game.ID]
		if !ok {
			continue
		}
		if xg.GameID != "" && xg.GameID != game.ID {
			return nil, fmt.Errorf("xG game ID %q does not match fixture %q", xg.GameID, game.ID)
		}
		if !validXG(xg.Home) || !validXG(xg.Away) {
			return nil, fmt.Errorf("invalid xG for game %q", game.ID)
		}
		h, hok := totals[game.HomeTeamID]
		a, aok := totals[game.AwayTeamID]
		if !hok || !aok {
			return nil, fmt.Errorf("completed game %q references an unknown team", game.ID)
		}
		h.played++
		h.forGoals += xg.Home
		h.against += xg.Away
		a.played++
		a.forGoals += xg.Away
		a.against += xg.Home
		totals[game.HomeTeamID] = h
		totals[game.AwayTeamID] = a
	}
	ids := make([]string, 0)
	matches := historical.XGMatches
	homeGoals, awayGoals := historical.HomeXG, historical.AwayXG
	for _, game := range leagueGames {
		if game.Status != standings.CompletedStatus {
			continue
		}
		xg, ok := leagueXG[game.ID]
		if !ok {
			continue
		}
		if xg.GameID != "" && xg.GameID != game.ID {
			return nil, fmt.Errorf("xG game ID %q does not match fixture %q", xg.GameID, game.ID)
		}
		if !validXG(xg.Home) || !validXG(xg.Away) {
			return nil, fmt.Errorf("invalid xG for game %q", game.ID)
		}
		matches++
		homeGoals += xg.Home
		awayGoals += xg.Away
		ids = append(ids, game.ID)
	}
	homeRate := (homeGoals + leaguePriorGames*priorHomeGoals) / (float64(matches) + leaguePriorGames)
	awayRate := (awayGoals + leaguePriorGames*priorAwayGoals) / (float64(matches) + leaguePriorGames)
	leagueRate := (homeRate + awayRate) / 2
	sort.Strings(ids)
	material := make([]byte, 0, len(ids)*24)
	for _, id := range ids {
		x := leagueXG[id]
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(id)))
		material = append(material, length[:]...)
		material = append(material, []byte(id)...)
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], math.Float64bits(x.Home))
		material = append(material, b[:]...)
		binary.BigEndian.PutUint64(b[:], math.Float64bits(x.Away))
		material = append(material, b[:]...)
	}
	if historical.XGMatches > 0 {
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], uint64(historical.XGMatches))
		material = append(material, b[:]...)
		binary.BigEndian.PutUint64(b[:], math.Float64bits(historical.HomeXG))
		material = append(material, b[:]...)
		binary.BigEndian.PutUint64(b[:], math.Float64bits(historical.AwayXG))
		material = append(material, b[:]...)
	}
	return xgPredictor{totals, homeRate, awayRate, leagueRate, material}, nil
}
func (p xgPredictor) SeedMaterial() []byte { return append([]byte(nil), p.material...) }
func (p xgPredictor) Distribution(game standings.Game) (Distribution, error) {
	if game.Status != "PreMatch" {
		return nil, fmt.Errorf("fixture %q is not remaining", game.ID)
	}
	h, hok := p.teams[game.HomeTeamID]
	a, aok := p.teams[game.AwayTeamID]
	if !hok || !aok {
		return nil, fmt.Errorf("fixture %q references an unknown team", game.ID)
	}
	ha, hd := xgStrengths(h, p.leagueRate)
	aa, ad := xgStrengths(a, p.leagueRate)
	return poissonDistribution{homeRate: clamp(p.homeRate * ha * ad), awayRate: clamp(p.awayRate * aa * hd)}, nil
}
func xgStrengths(t xgTotals, league float64) (float64, float64) {
	return ((t.forGoals + teamPriorGames*league) / (float64(t.played) + teamPriorGames)) / league, ((t.against + teamPriorGames*league) / (float64(t.played) + teamPriorGames)) / league
}
