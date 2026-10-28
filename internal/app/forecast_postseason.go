package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"

	"github.com/jrduncans/nwsl-season/internal/bracket"
	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/forecaststate"
	"github.com/jrduncans/nwsl-season/internal/simulation"
)

// forecastPostseason reads local cache only. The same input construction is
// used by live requests and scheduler warming, including conservative gaps.
func (a *application) forecastPostseason(ctx context.Context, season string, data cache.SeasonData, places int) (*simulation.Postseason, error) {
	remaining := false
	for _, game := range data.Games {
		if game.Status == fixtures.PreMatchStatus {
			remaining = true
		}
	}
	state := &simulation.Postseason{Seeds: map[int]string{}, Slots: map[string]simulation.PostseasonMatch{}}
	playoffData, err := a.loadSeasonData(ctx, season, "Playoffs")
	if err != nil {
		return nil, err
	}
	// A stale regular-season snapshot must not replay playoff rounds already
	// observed in the cache. Published fixtures, even with identified teams,
	// are not evidence that postseason play has started.
	playoffEvidence := false
	for _, game := range playoffData.Games {
		if game.Stage == "Playoffs" && playoffPlayStarted(game) {
			playoffEvidence = true
		}
	}
	if remaining && !playoffEvidence {
		return nil, nil
	}
	// Include every material source field (including shootouts and dates), even
	// when an alternate Store supplies no fixture snapshot ID. Raw payloads and
	// refresh timestamps are deliberately excluded.
	games := append([]cache.Game(nil), playoffData.Games...)
	sort.Slice(games, func(i, j int) bool { return games[i].ASAID < games[j].ASAID })
	for i := range games {
		games[i].RawJSON, games[i].LastUpdatedUTC = "", ""
	}
	material, _ := json.Marshal(games)
	digest := sha256.Sum256(material)
	state.SourceIdentity = hex.EncodeToString(digest[:])
	seeds := playoffSeedsFrom(season, data)
	if !seeds.Certified {
		state.Unavailable = "Championship odds are withheld until the complete regular-season inventory and final results can be verified."
		return state, nil
	}
	for seed := 1; seed <= places; seed++ {
		if seeds.Seeds[seed] == "" {
			state.Unavailable = "Championship odds are withheld because final playoff seeds are unresolved by the available official tiebreak data."
			return state, nil
		}
		state.Seeds[seed] = seeds.Seeds[seed]
	}
	entry, ok := competition.Lookup(season, "Playoffs")
	if !ok || forecastPlayoffBracket(season, places) == nil {
		state.Unavailable = "Championship odds are withheld because a compatible playoff format is not verified."
		return state, nil
	}
	for _, game := range games {
		if game.Season != season || game.Stage != "Playoffs" {
			state.Unavailable = "Championship odds are withheld because cached playoff data belongs to a different competition."
			return state, nil
		}
	}
	// Past this point every row belongs to this bracket, so recorded losses
	// stay provable even when another gap withholds the remaining odds.
	withhold := func(message string) (*simulation.Postseason, error) {
		state.Unavailable = message
		state.Eliminated = recordedPlayoffEliminations(*entry.BracketFormat, games, state.Seeds)
		return state, nil
	}
	failedRefresh, err := a.playoffRefreshWarning(ctx, season)
	if err != nil {
		return nil, err
	}
	if failedRefresh {
		return withhold("Championship odds are withheld because the latest playoff refresh did not succeed; published results may be newer than this cache. View the playoff bracket for the recorded results.")
	}
	for _, game := range games {
		// Unidentified placeholders carry no advancement. Any evidence of play
		// on such a row is a gap, not permission to restart that round.
		if game.HomeTeamID == "" || game.AwayTeamID == "" {
			if playoffPlayStarted(game) {
				return withhold("Championship odds are withheld because a playoff result has unidentified participants.")
			}
			continue
		}
		if !validPlayoffRow(game) {
			return withhold(unresolvedPlayoffResult)
		}
	}
	view := bracket.BuildWithSeeds(*entry.BracketFormat, games, state.Seeds)
	if view.State == bracket.StateFormatMismatch || view.State == bracket.StateUnresolved {
		return withhold(playoffBracketDisagreement)
	}
	for _, round := range view.Rounds {
		for _, slot := range round.Slots {
			if slot.Game == nil {
				continue
			}
			match := simulation.PostseasonMatch{Home: slot.Home.TeamID, Away: slot.Away.TeamID}
			if slot.Winner.TeamID != bracket.TBD {
				match.Winner = slot.Winner.TeamID
			} else if playoffPlayStarted(*slot.Game) {
				return withhold(unresolvedPlayoffResult)
			}
			state.Slots[slot.ID] = match
		}
	}
	// The simulator rejects progress it cannot follow; withhold with an
	// explanation instead of failing the page.
	check := simulation.Request{Teams: data.Teams, Games: standingsGames(data.Games), PlayoffPlaces: places, PlayoffBracket: forecastPlayoffBracket(season, places), Postseason: state}
	if simulation.CheckPostseason(check) != nil {
		return withhold(playoffBracketDisagreement)
	}
	return state, nil
}

const (
	unresolvedPlayoffResult    = "Championship odds are withheld until the unresolved playoff result has a confirmed advancing team."
	playoffBracketDisagreement = "Championship odds are withheld because published playoff matches or advancement are unresolved or disagree with the verified bracket."
)

// playoffPlayStarted reports any source evidence that a playoff row has been
// played or is underway, as opposed to a scheduled fixture or placeholder.
func playoffPlayStarted(game cache.Game) bool {
	return game.Status != fixtures.PreMatchStatus && game.Status != "" || game.HomeScore.Valid || game.AwayScore.Valid || game.HomePenalties.Valid || game.AwayPenalties.Valid || (game.Penalties.Valid && game.Penalties.Bool) || (game.ExtraTime.Valid && game.ExtraTime.Bool)
}

// validPlayoffRow accepts an identified row that is either cleanly unplayed or
// completed with a decisive score or a decisive shootout after a draw.
func validPlayoffRow(game cache.Game) bool {
	shootout := game.Penalties.Valid && game.Penalties.Bool
	switch game.Status {
	case fixtures.PreMatchStatus:
		return !playoffPlayStarted(game)
	case fixtures.CompletedStatus:
		valid := game.HomeScore.Valid && game.AwayScore.Valid && game.HomeScore.Int64 >= 0 && game.AwayScore.Int64 >= 0
		if shootout {
			return valid && game.HomeScore.Int64 == game.AwayScore.Int64 && game.HomePenalties.Valid && game.AwayPenalties.Valid && game.HomePenalties.Int64 >= 0 && game.AwayPenalties.Int64 >= 0 && game.HomePenalties.Int64 != game.AwayPenalties.Int64
		}
		return valid && !game.HomePenalties.Valid && !game.AwayPenalties.Valid
	}
	return false
}

// recordedPlayoffEliminations lists qualifiers knocked out by completed,
// valid results. Other rows are left out rather than guessed, and a bracket
// that still disagrees proves nothing.
func recordedPlayoffEliminations(format competition.BracketFormat, games []cache.Game, seeds map[int]string) []string {
	completed := make([]cache.Game, 0, len(games))
	for _, game := range games {
		if game.HomeTeamID != "" && game.AwayTeamID != "" && game.Status == fixtures.CompletedStatus && validPlayoffRow(game) && fixtureWinner(game) != "" {
			completed = append(completed, game)
		}
	}
	view := bracket.BuildWithSeeds(format, completed, seeds)
	if view.State == bracket.StateFormatMismatch || view.State == bracket.StateUnresolved {
		return nil
	}
	var eliminated []string
	for _, round := range view.Rounds {
		for _, slot := range round.Slots {
			if slot.Game == nil || slot.Winner.TeamID == bracket.TBD {
				continue
			}
			loser := slot.Home.TeamID
			if loser == slot.Winner.TeamID {
				loser = slot.Away.TeamID
			}
			eliminated = append(eliminated, loser)
		}
	}
	sort.Strings(eliminated)
	return eliminated
}

func (a *application) renderStaleForecast(w http.ResponseWriter, r *http.Request, scope requestCompetition, state forecaststate.State) {
	state.Fixed = map[string]simulation.Outcome{}
	state.PlayoffWinners = nil
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	a.render(w, "error", errorPage{
		Title:        "This forecast scenario is out of date",
		Message:      "A fixed result no longer matches the available fixtures. Shared links use the latest cache; completed, removed, or changed matchups cannot stay fixed. Open the latest forecast to keep your model choices and clear those assumptions.",
		RecoveryPath: forecastURL(r.URL.Path, scope.Season, scope.Entry.Slug, state, ""),
		HomePath:     relativeURL(r.URL.Path, stageURL(scope.Season, scope.Entry.Slug)), StylesheetPath: staticURL(r.URL.Path, "site.css"), ScriptPath: staticURL(r.URL.Path, "standings.js"),
	})
}
