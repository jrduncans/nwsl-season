package forecast

import (
	"encoding/binary"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/jrduncans/nwsl-season/internal/standings"
)

const (
	xgPoissonRecentFormID = "xg-poisson-recent-form-v1"

	// recentFormHalfLife is deliberately expressed in days rather than matches:
	// it keeps the meaning of the model stable through international breaks and
	// uneven schedules. A match 60 days old counts half as much as the latest
	// completed match, and one 120 days old counts one quarter as much.
	recentFormHalfLife = 60 * 24 * time.Hour
)

// NewXGPoissonRecentFormV1 is an experimental xG model that gives recent
// current-season matches more influence over a team's attack and defence.
func NewXGPoissonRecentFormV1() Model { return xgPoissonRecentFormV1{} }

type xgPoissonRecentFormV1 struct{}

func (xgPoissonRecentFormV1) Info() Info {
	return Info{
		ID:          xgPoissonRecentFormID,
		Name:        "xG Poisson (recent form)",
		Description: "An experimental xG Poisson model that gives more weight to each team's recent matches.",
		Inputs:      "Available ASA team-model xG from the current season, weighted by match date; two previous regular seasons plus the current season set league home and away xG rates.",
		Assumptions: "A current-season match loses half its influence every 60 days. The stable league home-field baseline remains pooled across two earlier seasons, and the usual eight-match prior limits sparse recent data.",
	}
}

type weightedXGTotals struct {
	weight, forGoals, against float64
}

type recentFormXGPredictor struct {
	teams                          map[string]weightedXGTotals
	homeRate, awayRate, leagueRate float64
	material                       []byte
}

func (xgPoissonRecentFormV1) Fit(input FitInput) (Predictor, error) {
	base, err := fitXGPoissonHomeTwoSeasons(input)
	if err != nil {
		return nil, err
	}
	venue := base.(xgPredictor)
	known, err := validateTeams(input.Teams)
	if err != nil {
		return nil, err
	}
	cutoff := latestCompletedKickoff(input.Games)
	totals := make(map[string]weightedXGTotals, len(known))
	for id := range known {
		totals[id] = weightedXGTotals{}
	}

	ids := make([]string, 0)
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
		home, homeKnown := totals[game.HomeTeamID]
		away, awayKnown := totals[game.AwayTeamID]
		if !homeKnown || !awayKnown {
			return nil, fmt.Errorf("completed game %q references an unknown team", game.ID)
		}
		weight := recencyWeight(cutoff, game.Kickoff)
		home.weight += weight
		home.forGoals += weight * xg.Home
		home.against += weight * xg.Away
		away.weight += weight
		away.forGoals += weight * xg.Away
		away.against += weight * xg.Home
		totals[game.HomeTeamID] = home
		totals[game.AwayTeamID] = away
		ids = append(ids, game.ID)
	}

	return recentFormXGPredictor{
		teams: totals, homeRate: venue.homeRate, awayRate: venue.awayRate, leagueRate: venue.leagueRate,
		material: appendRecencyMaterial(venue.material, cutoff, input.Games, input.XGoals, ids),
	}, nil
}

// fitXGPoissonHomeTwoSeasons retains the established venue-rate window. The
// recency experiment changes team form only; a small, noisy run of matches
// should not also redefine league-wide home advantage.
func fitXGPoissonHomeTwoSeasons(input FitInput) (Predictor, error) {
	if input.HistoricalVenue.XGMatches > 0 {
		return fitXGPoisson(input, input.Games, input.XGoals, input.HistoricalVenue)
	}
	history, xgoals := historyPool(input, 2)
	games := append(history, input.Games...)
	for id, xg := range input.XGoals {
		xgoals[id] = xg
	}
	return fitXGPoisson(input, games, xgoals, VenueSample{})
}

func (p recentFormXGPredictor) SeedMaterial() []byte { return append([]byte(nil), p.material...) }

func (p recentFormXGPredictor) Distribution(game standings.Game) (Distribution, error) {
	if game.Status != "PreMatch" {
		return nil, fmt.Errorf("fixture %q is not remaining", game.ID)
	}
	home, homeKnown := p.teams[game.HomeTeamID]
	away, awayKnown := p.teams[game.AwayTeamID]
	if !homeKnown || !awayKnown {
		return nil, fmt.Errorf("fixture %q references an unknown team", game.ID)
	}
	homeAttack, homeDefence := weightedXGStrengths(home, p.leagueRate)
	awayAttack, awayDefence := weightedXGStrengths(away, p.leagueRate)
	return poissonDistribution{
		homeRate: clamp(p.homeRate * homeAttack * awayDefence),
		awayRate: clamp(p.awayRate * awayAttack * homeDefence),
	}, nil
}

func weightedXGStrengths(t weightedXGTotals, league float64) (float64, float64) {
	return ((t.forGoals + teamPriorGames*league) / (t.weight + teamPriorGames)) / league,
		((t.against + teamPriorGames*league) / (t.weight + teamPriorGames)) / league
}

func latestCompletedKickoff(games []standings.Game) time.Time {
	var latest time.Time
	for _, game := range games {
		if game.Status == standings.CompletedStatus && game.Kickoff.After(latest) {
			latest = game.Kickoff
		}
	}
	return latest
}

func recencyWeight(cutoff, kickoff time.Time) float64 {
	// Some direct callers and old test fixtures do not contain kickoffs. Keep
	// those observations usable at full weight rather than silently discarding
	// them; production and backtest fixture inputs always have a kickoff time.
	if cutoff.IsZero() || kickoff.IsZero() || !cutoff.After(kickoff) {
		return 1
	}
	return math.Exp(-math.Ln2 * cutoff.Sub(kickoff).Hours() / recentFormHalfLife.Hours())
}

func appendRecencyMaterial(base []byte, cutoff time.Time, games []standings.Game, xgoals map[string]ExpectedGoals, ids []string) []byte {
	material := append([]byte(nil), base...)
	material = append(material, "recent-form-xg-v1"...)
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(cutoff.UnixNano()))
	material = append(material, b[:]...)
	sort.Strings(ids)
	for _, id := range ids {
		for _, game := range games {
			if game.ID != id {
				continue
			}
			binary.BigEndian.PutUint64(b[:], uint64(len(id)))
			material = append(material, b[:]...)
			material = append(material, id...)
			binary.BigEndian.PutUint64(b[:], uint64(game.Kickoff.UnixNano()))
			material = append(material, b[:]...)
			xg := xgoals[id]
			binary.BigEndian.PutUint64(b[:], math.Float64bits(xg.Home))
			material = append(material, b[:]...)
			binary.BigEndian.PutUint64(b[:], math.Float64bits(xg.Away))
			material = append(material, b[:]...)
			break
		}
	}
	return material
}
