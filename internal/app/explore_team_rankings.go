package app

import "fmt"

type exploreTeamRank struct {
	Label    string `json:"label"`
	Value    string `json:"value"`
	Rank     string `json:"rank"`
	Position string `json:"position"`
}

type exploreTeamRankingsView struct {
	RankingTeam      teamNameView
	RankingRows      []exploreTeamRank
	RankingTeamCount int
	RankingPlayed    int
	RankingActive    bool
	RankingMissingXG bool
}

// Calculate both unit modes once from the same eligible season population.
// Expected ranks require coverage for every team, so missing observations can
// never make a team appear to rank higher in the league.
func populateExploreTeamRanks(teams []exploreTeamRecord) {
	for unitIndex, units := range []string{"per-match", "total"} {
		for expectedIndex, labels := range [][]string{
			{"Goals scored", "Goals allowed", "Goal differential"},
			{"xG", "xG allowed", "xG differential"},
		} {
			for measure, label := range labels {
				values := make([]*float64, len(teams))
				complete := true
				for i, team := range teams {
					value := team.Values[measure]
					if units == "total" {
						value = team.Totals[measure]
					}
					if expectedIndex == 1 {
						values[i] = value.Expected
					} else {
						values[i] = &value.Actual
					}
					complete = complete && values[i] != nil
				}
				for i := range teams {
					row := exploreTeamRank{Label: label, Value: "Unavailable", Rank: "Unavailable"}
					if values[i] != nil {
						row.Value = fmt.Sprintf("%.2f", *values[i])
						if units == "total" && expectedIndex == 0 {
							row.Value = fmt.Sprintf("%.0f", *values[i])
						}
					}
					if complete {
						rank, tied := 1, false
						for j, value := range values {
							if (measure == 1 && *value < *values[i]) || (measure != 1 && *value > *values[i]) {
								rank++
							}
							tied = tied || (i != j && *value == *values[i])
						}
						row.Rank = ordinalRank(rank)
						if tied {
							row.Rank = "Tied " + row.Rank
						}
						position := 0.0
						if len(teams) > 1 {
							position = 100 * float64(rank-1) / float64(len(teams)-1)
						}
						row.Position = fmt.Sprintf("%.4f", position)
					}
					teams[i].Rankings[unitIndex][expectedIndex*3+measure] = row
				}
			}
		}
	}
}

func ordinalRank(rank int) string {
	suffix := "th"
	if rank%100 < 11 || rank%100 > 13 {
		switch rank % 10 {
		case 1:
			suffix = "st"
		case 2:
			suffix = "nd"
		case 3:
			suffix = "rd"
		}
	}
	return fmt.Sprintf("%d%s", rank, suffix)
}

func exploreTeamRankings(teams exploreTeamsView, selected teamNameView) exploreTeamRankingsView {
	page := exploreTeamRankingsView{RankingTeam: selected}
	for _, season := range teams.TeamSeasons {
		if season.Season != teams.TeamSeason {
			continue
		}
		page.RankingActive = season.Active
		page.RankingTeamCount = len(season.Teams)
		for _, team := range season.Teams {
			page.RankingMissingXG = page.RankingMissingXG || team.Values[0].Expected == nil
			if team.ID == selected.ID {
				page.RankingTeam = teamNameView{ID: team.ID, Name: team.Name, LogoURL: team.LogoURL}
				page.RankingPlayed = team.Played
				unitIndex := 0
				if teams.TeamUnits == "total" {
					unitIndex = 1
				}
				page.RankingRows = team.Rankings[unitIndex][:]
			}
		}
	}
	return page
}
