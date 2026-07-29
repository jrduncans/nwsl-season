package forecast

const (
	xgPoissonHomeTwoSeasonsID = "xg-poisson-home-two-seasons-v1"
	xgPoissonHomeHistoryID    = "xg-poisson-home-history-v1"
)

// NewXGPoissonHomeHistoryV1 tests historical league home and away xG rates
// while retaining each current team's season-to-date xG strengths.
func NewXGPoissonHomeHistoryV1() Model { return xgPoissonHomeHistoryV1{} }

// NewXGPoissonHomeTwoSeasonsV1 uses the two most recent completed regular
// seasons plus current-season xG for league venue rates.
func NewXGPoissonHomeTwoSeasonsV1() Model { return xgPoissonHomeHistoryV1{seasons: 2} }

type xgPoissonHomeHistoryV1 struct{ seasons int }

func (p xgPoissonHomeHistoryV1) Info() Info {
	scope, id, name := "all earlier completed seasons", xgPoissonHomeHistoryID, "xG Poisson (all home/away history)"
	if p.seasons == 2 {
		scope, id, name = "the two most recent completed regular seasons", xgPoissonHomeTwoSeasonsID, "xG Poisson"
	}
	return Info{
		ID:          id,
		Name:        name,
		Description: "Simulates remaining games from current-season team xG, with league home and away xG rates pooled across " + scope + ".",
		Inputs:      "Available ASA xG from " + scope + ", current-season xG, and the remaining schedule.",
		Assumptions: "League home-field xG is stable enough to pool across prior seasons; team attack and defence are not pooled.",
	}
}

func (p xgPoissonHomeHistoryV1) Fit(input FitInput) (Predictor, error) {
	if p.seasons == 2 {
		return fitXGPoissonHomeTwoSeasons(input)
	}
	history, xgoals := historyPool(input, p.seasons)
	games := append(history, input.Games...)
	for id, xg := range input.XGoals {
		xgoals[id] = xg
	}
	return fitXGPoisson(input, games, xgoals, VenueSample{})
}
