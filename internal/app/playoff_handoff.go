package app

import (
	"context"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

type playoffSeeds struct {
	Complete bool
	Seeds    map[int]string
	Teams    []standings.Team
}

// finalPlayoffSeeds reads only the local regular-season snapshot. Inventory
// finality and official total ranking are separate gates: a completed season
// can expose Playoffs while individual tied seeds remain unknown.
func (a *application) finalPlayoffSeeds(ctx context.Context, season string) (playoffSeeds, error) {
	regular, ok := competition.Lookup(season, "Regular Season")
	if !ok || !regular.Supports(competition.CapabilityStandings) {
		return playoffSeeds{}, nil
	}
	data, err := a.loadSeasonData(ctx, season, regular.Stage)
	if err != nil {
		return playoffSeeds{}, err
	}
	// Also guard Store implementations that return an unscoped snapshot.
	for _, game := range data.Games {
		if game.Season != season || game.Stage != regular.Stage {
			return playoffSeeds{}, nil
		}
	}
	presentation := classifySeasonPhase(data, regular.Inventory)
	if presentation.Phase != seasonPhaseComplete || !presentation.FinalStandingsSafe {
		return playoffSeeds{}, nil
	}
	result := playoffSeeds{Complete: true, Teams: data.Teams, Seeds: map[int]string{}}
	table, err := competition.OfficialStandings(season, data.Teams, standingsGames(data.Games), standings.OrderOfficialTotal)
	if err == nil {
		for i, row := range table {
			if !row.TieBreak.Undetermined {
				result.Seeds[i+1] = row.Team.ID
			}
		}
	}
	return result, nil
}

const playoffPublicationNotice = "Playoff schedule details are awaiting ASA publication or discovery in the local cache. Missing cached details do not establish whether ASA has published them."

func (a *application) playoffPublicationStatus(ctx context.Context, season string, data cache.SeasonData, ready bool) (string, error) {
	notice := playoffPublicationNotice
	if ready {
		notice = "ASA-published fixtures and results are shown below."
	} else if len(data.Games) > 0 {
		notice += " ASA fixtures may be placeholders or partially published; only identified matchups are placed below."
	}
	status, err := a.store.Status(ctx, season, "Playoffs")
	if err != nil {
		return "", err
	}
	if status.LastAttempt != nil && status.LastAttempt.Outcome != "success" {
		notice += " The latest refresh did not succeed; published details may be newer than this cache."
	}
	return notice, nil
}
