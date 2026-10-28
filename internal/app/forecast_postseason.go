package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"slices"
	"sort"
	"strconv"

	"github.com/jrduncans/nwsl-season/internal/bracket"
	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/forecaststate"
	"github.com/jrduncans/nwsl-season/internal/simulation"
	"github.com/jrduncans/nwsl-season/internal/standings"
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
	// observed in the cache. Empty upcoming placeholders alone are not evidence
	// that postseason forecasting has started.
	playoffEvidence := false
	for _, game := range playoffData.Games {
		if game.Stage == "Playoffs" && (game.HomeTeamID != "" || game.AwayTeamID != "" || game.HomeScore.Valid || game.AwayScore.Valid || game.Status == fixtures.CompletedStatus) {
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
			state.Unavailable = "Championship odds are withheld because final playoff seeds are unresolved by the available official tiebreak data. Display order is not an official seed."
			return state, nil
		}
		state.Seeds[seed] = seeds.Seeds[seed]
	}
	entry, ok := competition.Lookup(season, "Playoffs")
	if !ok || forecastPlayoffBracket(season, places) == nil {
		state.Unavailable = "Championship odds are withheld because a compatible playoff format is not verified."
		return state, nil
	}
	failedRefresh, err := a.playoffRefreshWarning(ctx, season)
	if err != nil {
		return nil, err
	}
	if failedRefresh {
		state.Unavailable = "Championship odds are withheld because the latest playoff refresh did not succeed; published results may be newer than this cache. View the playoff bracket for the recorded results."
		return state, nil
	}
	for _, game := range games {
		if game.Season != season || game.Stage != "Playoffs" {
			state.Unavailable = "Championship odds are withheld because cached playoff data belongs to a different competition."
			return state, nil
		}
		// Unidentified placeholders carry no advancement. Any evidence of play
		// on such a row is a gap, not permission to restart that round.
		if game.HomeTeamID == "" || game.AwayTeamID == "" {
			if game.Status != fixtures.PreMatchStatus && game.Status != "" || game.HomeScore.Valid || game.AwayScore.Valid || game.HomePenalties.Valid || game.AwayPenalties.Valid || (game.Penalties.Valid && game.Penalties.Bool) || (game.ExtraTime.Valid && game.ExtraTime.Bool) {
				state.Unavailable = "Championship odds are withheld because a playoff result has unidentified participants."
				return state, nil
			}
			continue
		}
		valid := false
		switch game.Status {
		case fixtures.PreMatchStatus:
			hasShootout := game.Penalties.Valid && game.Penalties.Bool
			valid = !game.HomeScore.Valid && !game.AwayScore.Valid && !game.HomePenalties.Valid && !game.AwayPenalties.Valid && !hasShootout
		case fixtures.CompletedStatus:
			valid = game.HomeScore.Valid && game.AwayScore.Valid && game.HomeScore.Int64 >= 0 && game.AwayScore.Int64 >= 0
			if game.Penalties.Valid && game.Penalties.Bool {
				valid = valid && game.HomeScore.Int64 == game.AwayScore.Int64 && game.HomePenalties.Valid && game.AwayPenalties.Valid && game.HomePenalties.Int64 >= 0 && game.AwayPenalties.Int64 >= 0 && game.HomePenalties.Int64 != game.AwayPenalties.Int64
			} else if game.HomePenalties.Valid || game.AwayPenalties.Valid {
				valid = false
			}
		}
		if !valid {
			state.Unavailable = "Championship odds are withheld until the unresolved playoff result has a confirmed advancing team. Completed rounds will not be replayed."
			return state, nil
		}
	}
	view := bracket.BuildWithSeeds(*entry.BracketFormat, games, state.Seeds)
	if view.State == bracket.StateFormatMismatch || view.State == bracket.StateUnresolved {
		state.Unavailable = "Championship odds are withheld because published playoff matches or advancement are unresolved or disagree with the verified bracket. Completed rounds will not be replayed."
		return state, nil
	}
	for _, round := range view.Rounds {
		for _, slot := range round.Slots {
			if slot.Game == nil {
				continue
			}
			match := simulation.PostseasonMatch{Home: slot.Home.TeamID, Away: slot.Away.TeamID}
			if slot.Winner.TeamID != bracket.TBD {
				match.Winner = slot.Winner.TeamID
			} else if slot.Game.Status != fixtures.PreMatchStatus || slot.Game.HomeScore.Valid || slot.Game.AwayScore.Valid || slot.Game.HomePenalties.Valid || slot.Game.AwayPenalties.Valid {
				state.Unavailable = "Championship odds are withheld until the unresolved playoff result has a confirmed advancing team. Completed rounds will not be replayed."
				return state, nil
			}
			state.Slots[slot.ID] = match
		}
	}
	return state, nil
}

func (p *forecastPage) applyPostseason(data cache.SeasonData, state *simulation.Postseason, places int, fromPath, season string) {
	if state == nil {
		return
	}
	p.PostseasonWarning = state.Unavailable
	p.PlayoffsPath = relativeURL(fromPath, stageURL(season, "playoffs"))
	regular, _ := competition.Lookup(season, "Regular Season")
	p.Postseason = finalStandingsSafe(classifySeasonPhase(data, regular.Inventory))
	if !p.Postseason {
		return
	}
	p.PostseasonNotice = "The regular season is final. Initial pairings follow resolved final seeds and the verified bracket. Unpublished dates and venues remain unknown. Cached playoff winners advance as recorded; only unresolved rounds are simulated."
	if len(state.Slots) == 0 && state.Unavailable == "" {
		p.PostseasonNotice += " " + playoffPublicationNotice
	}
	table, err := competition.OfficialStandings(season, data.Teams, standingsGames(data.Games), standings.OrderOfficialTotal)
	if err != nil {
		return
	}
	rows := make(map[string]forecastRowView, len(p.Rows))
	for _, row := range p.Rows {
		rows[row.Team.ID] = row
	}
	p.Rows = nil
	for i, official := range table {
		row := rows[official.Team.ID]
		row.FinalPoints = strconv.Itoa(official.Record.Points)
		row.FinalSeed = strconv.Itoa(i + 1)
		row.Qualification = "Did not qualify"
		if i < places {
			row.Qualification = "Qualified"
		}
		if official.TieBreak.Undetermined {
			row.FinalSeed = "Unresolved"
			// All occupied positions must agree before claiming qualification.
			for j, other := range table {
				if slices.Contains(official.TieBreak.TiedTeamIDs, other.Team.ID) && (j < places) != (i < places) {
					row.Qualification = "Unresolved"
				}
			}
		}
		p.Rows = append(p.Rows, row)
	}
}

func (a *application) renderStaleForecast(w http.ResponseWriter, r *http.Request, scope requestCompetition, state forecaststate.State) {
	state.Fixed = map[string]simulation.Outcome{}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	a.render(w, "error", errorPage{
		Title:        "This forecast scenario is out of date",
		Message:      "A fixed regular-season result is no longer a remaining cached match. Shared links use the latest cache; completed or removed matches cannot stay fixed. Open the latest forecast to keep your model choices and clear those assumptions.",
		RecoveryPath: forecastURL(r.URL.Path, scope.Season, scope.Entry.Slug, state, ""),
		HomePath:     relativeURL(r.URL.Path, stageURL(scope.Season, scope.Entry.Slug)), StylesheetPath: staticURL(r.URL.Path, "site.css"), ScriptPath: staticURL(r.URL.Path, "standings.js"),
	})
}
