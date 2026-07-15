package forecast

import (
	"encoding/binary"
	"fmt"
	"github.com/jrduncans/nwsl-season/internal/standings"
	"math"
	"sort"
)

const xgPoissonID = "xg-poisson-v1"

func NewXGPoissonV1() Model { return xgPoissonV1{} }

type xgPoissonV1 struct{}

func (xgPoissonV1) Info() Info {
	return Info{ID: xgPoissonID, Name: "xG Poisson", Description: "ASA team-model expected goals with the Results Poisson formula.", Inputs: "Available ASA team-model xG for completed matches and the published fixture list.", Assumptions: "Independent Poisson scoring, league/team shrinkage, and observed home advantage; missing xG is not replaced with scores.", MethodPath: "/docs/11-xg-and-models.md#xg-poisson-v1"}
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
	known, err := validateTeams(input.Teams)
	if err != nil {
		return nil, err
	}
	totals := map[string]xgTotals{}
	for id := range known {
		totals[id] = xgTotals{}
	}
	ids := make([]string, 0)
	var matches int
	var homeGoals, awayGoals float64
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
		x := input.XGoals[id]
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
