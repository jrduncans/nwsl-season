package app

import (
	"context"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

// playoffSeeds holds final seeds once the regular season is safely complete.
// Certified covers the season as a whole; individual tied seeds stay absent.
type playoffSeeds struct {
	Certified bool
	Seeds     map[int]string
	Teams     []standings.Team
}

// finalPlayoffSeeds reads only the local regular-season snapshot.
func (a *application) finalPlayoffSeeds(ctx context.Context, season string) (playoffSeeds, error) {
	regular, ok := competition.Lookup(season, "Regular Season")
	if !ok || !regular.Supports(competition.CapabilityStandings) {
		return playoffSeeds{}, nil
	}
	data, err := a.loadSeasonData(ctx, season, regular.Stage)
	if err != nil {
		return playoffSeeds{}, err
	}
	return playoffSeedsFrom(season, data), nil
}

// playoffSeedsFrom derives final seeds from already loaded regular-season
// data. Inventory finality and official total ranking are separate gates: a
// completed season can expose Playoffs while individual tied seeds remain
// unknown.
func playoffSeedsFrom(season string, data cache.SeasonData) playoffSeeds {
	regular, ok := competition.Lookup(season, "Regular Season")
	if !ok || !regular.Supports(competition.CapabilityStandings) {
		return playoffSeeds{}
	}
	// Also guard Store implementations that return an unscoped snapshot.
	for _, game := range data.Games {
		if game.Season != season || game.Stage != regular.Stage {
			return playoffSeeds{}
		}
	}
	if !finalStandingsSafe(classifySeasonPhase(data, regular.Inventory)) {
		return playoffSeeds{}
	}
	result := playoffSeeds{Certified: true, Teams: data.Teams, Seeds: map[int]string{}}
	table, err := competition.OfficialStandings(season, data.Teams, standingsGames(data.Games), standings.OrderOfficialTotal)
	if err == nil {
		for i, row := range table {
			if !row.TieBreak.Undetermined {
				result.Seeds[i+1] = row.Team.ID
			}
		}
	}
	return result
}

// finalStandingsSafe reports a completed season whose final results can be
// trusted for seeding and the playoff handoff.
func finalStandingsSafe(presentation seasonPresentation) bool {
	return presentation.Phase == seasonPhaseComplete && presentation.FinalStandingsSafe
}

// seedsDrivePlayoffBracket reports a season whose verified rules let final
// seeds shape the playoff bracket. Historical seasons have no verified rules
// and keep source-only brackets.
func seedsDrivePlayoffBracket(regular competition.Entry) bool {
	return regular.Rules != nil
}

// playoffBracketEntry reports a playoff stage the catalog verifies as a bracket.
func playoffBracketEntry(entry competition.Entry) bool {
	return entry.Stage == "Playoffs" && entry.Supports(competition.CapabilityBracket)
}

const playoffPublicationNotice = "ASA hasn't published the playoff schedule yet, or this site hasn't picked it up."

// playoffWaitingNotice explains a bracket still waiting on ASA. A fully
// published bracket needs no notice.
func playoffWaitingNotice(data cache.SeasonData, ready bool) string {
	switch {
	case ready:
		return ""
	case len(data.Games) > 0:
		return playoffPublicationNotice
	default:
		return playoffPublicationNotice
	}
}

// playoffRefreshWarning reports whether the latest playoff refresh failed, so
// published details may be newer than the cache.
func (a *application) playoffRefreshWarning(ctx context.Context, season string) (bool, error) {
	status, err := a.store.Status(ctx, season, "Playoffs")
	if err != nil {
		return false, err
	}
	return status.LastAttempt != nil && status.LastAttempt.Outcome != "success", nil
}
