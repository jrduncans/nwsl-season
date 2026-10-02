package app

import (
	"fmt"
	"slices"
	"strings"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/simulation"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

type exploreTrendMean struct {
	Value *float64 `json:"value"`
	Count int      `json:"count"`
	Total int      `json:"total"`
	Names []string `json:"names"`
}

type exploreTrendBenchmark struct {
	Key   string                 `json:"key"`
	Label string                 `json:"label"`
	Note  string                 `json:"note"`
	Means [4][2]exploreTrendMean `json:"means"`
}

type exploreTrendReferenceOption struct{ Key, Label string }
type exploreTrendReferenceView struct{ Label, Value, Coverage, Members, Note string }

// Means use raw team match appearances, independent of the chart's window.
// xG and xPoints have separate coverage; neither is filled from actual results.
func exploreTrendMeanFor(teams []exploreTeamRecord, measure int, expected bool) exploreTrendMean {
	mean := exploreTrendMean{}
	total := 0.0
	for _, team := range teams {
		mean.Names = append(mean.Names, team.Name)
		for _, match := range team.Matches {
			mean.Total++
			value := match.Values[measure].Actual
			if expected {
				if match.Values[measure].Expected == nil {
					continue
				}
				value = *match.Values[measure].Expected
			}
			mean.Count++
			total += value
		}
	}
	if mean.Count > 0 {
		value := total / float64(mean.Count)
		mean.Value = &value
	}
	return mean
}

func exploreTrendBenchmarks(season exploreTeamSeason, input cache.HistoricalSeason, projection ...*simulation.Result) []exploreTrendBenchmark {
	labels := []string{"League average", "Playoff-team average", "Top 4 average", "Shield winner", "Best team average"}
	if season.Active {
		labels[1] = fmt.Sprintf("Projected top %d average", input.Entry.PlayoffPlaces)
		labels[2], labels[3] = "Projected top 4 average", "Shield favorite"
	}
	groups := make(map[string][]exploreTeamRecord)
	groups["league"] = season.Teams
	groupNote := "Membership follows the total-points standings. Includes this team when it belongs to the group."
	if season.Active {
		groupNote = "Membership uses the default Forecast Lab model's projected final standings. Reference values use recorded matches so far."
	}
	groupUnavailable := ""
	if !input.Entry.Supports(competition.CapabilityStandings) {
		groupUnavailable = "Standings are unavailable for this season."
	} else if !season.Active && slices.ContainsFunc(input.Data.Games, func(game cache.Game) bool {
		return game.Status != fixtures.CompletedStatus && game.Status != fixtures.AbandonedStatus
	}) {
		groupUnavailable = "Final standings are unavailable while fixtures remain unplayed."
	}
	if groupUnavailable == "" && !season.Active {
		table := standings.Calculate(input.Data.Teams, standingsGames(input.Data.Games), standings.OfficialTotalRules())
		for key, size := range map[string]int{"playoff": input.Entry.PlayoffPlaces, "top-four": 4, "shield": 1} {
			if size <= 0 || size > len(table) {
				continue
			}
			// Historical catalogs have no season-specific tiebreak rules. A
			// points tie across their cut cannot certify a factual final group.
			if input.Entry.Rules == nil && size < len(table) && table[size-1].Record.Points == table[size].Record.Points {
				continue
			}
			// A deterministic display tie must not invent membership across a cut.
			selected := table[:size]
			ids := make(map[string]bool, size)
			for _, row := range selected {
				ids[row.Team.ID] = true
			}
			unresolved := slices.ContainsFunc(selected, func(row standings.TableRow) bool {
				return row.TieBreak.Undetermined && slices.ContainsFunc(row.TieBreak.TiedTeamIDs, func(id string) bool { return !ids[id] })
			})
			if unresolved {
				continue
			}
			for _, row := range selected {
				for _, team := range season.Teams {
					if team.ID == row.Team.ID {
						groups[key] = append(groups[key], team)
						break
					}
				}
			}
			if len(groups[key]) != size {
				delete(groups, key)
			}
		}
	}
	if season.Active && len(projection) > 0 && projection[0] != nil {
		groups = exploreProjectedGroups(season, input.Entry.PlayoffPlaces, *projection[0])
		groups["league"] = season.Teams
		groupNote += " Model: " + projection[0].Model.Name + "."
	}
	benchmarks := make([]exploreTrendBenchmark, 0, len(labels))
	for index, key := range []string{"league", "playoff", "top-four", "shield", "best"} {
		benchmark := exploreTrendBenchmark{Key: key, Label: labels[index], Note: groupNote}
		if key == "league" {
			benchmark.Note = "All teams, weighted by recorded match appearances. Expected means use available observations."
		}
		if key == "shield" && season.Active {
			benchmark.Note = "Shield favorite has the highest Shield probability in the default Forecast Lab model. Equal favorites include every tied team. Reference values use recorded matches so far."
			if len(projection) > 0 && projection[0] != nil {
				benchmark.Note += " Model: " + projection[0].Model.Name + "."
			}
		}
		if key != "league" && key != "best" && len(groups[key]) == 0 {
			benchmark.Note = "Membership unavailable: insufficient teams or a standings tie at the cut line without verified tiebreak resolution."
			if season.Active {
				benchmark.Note = "The default Forecast Lab projection is unavailable for this season. Reference values require every selected team to have recorded matches."
			}
			if groupUnavailable != "" {
				benchmark.Note = groupUnavailable
			}
		}
		if key == "best" {
			benchmark.Note = "Best season average for each metric; lower is better for Allowed. Expected comparisons require complete coverage for every team. Tied holders are all listed."
		}
		for measure := range 4 {
			for basis := range 2 {
				members := groups[key]
				if key == "best" {
					members = exploreTrendBestTeams(season.Teams, measure, basis == 1, len(input.Data.Teams))
				}
				benchmark.Means[measure][basis] = exploreTrendMeanFor(members, measure, basis == 1)
			}
		}
		if key == "league" {
			// Both panels use the identical paired-fixture league benchmark,
			// including its unrounded value and coverage counts.
			benchmark.Means[1] = benchmark.Means[0]
		}
		benchmarks = append(benchmarks, benchmark)
	}
	return benchmarks
}

func exploreProjectedGroups(season exploreTeamSeason, places int, result simulation.Result) map[string][]exploreTeamRecord {
	groups := make(map[string][]exploreTeamRecord)
	rows := append([]simulation.TeamResult(nil), result.Teams...)
	// Match Forecast Lab's displayed projected standings, including tie order.
	slices.SortStableFunc(rows, func(a, b simulation.TeamResult) int {
		if a.ExpectedPoints != b.ExpectedPoints {
			if a.ExpectedPoints > b.ExpectedPoints {
				return -1
			}
			return 1
		}
		if a.PlayoffProbability != b.PlayoffProbability {
			if a.PlayoffProbability > b.PlayoffProbability {
				return -1
			}
			return 1
		}
		if a.TopFourProbability != b.TopFourProbability {
			if a.TopFourProbability > b.TopFourProbability {
				return -1
			}
			return 1
		}
		if names := strings.Compare(standings.DisplayName(a.Team), standings.DisplayName(b.Team)); names != 0 {
			return names
		}
		return strings.Compare(a.Team.ID, b.Team.ID)
	})
	ids := make(map[string]exploreTeamRecord, len(season.Teams))
	for _, team := range season.Teams {
		ids[team.ID] = team
	}
	for key, size := range map[string]int{"playoff": places, "top-four": 4} {
		if size <= 0 || size > len(rows) {
			continue
		}
		for _, row := range rows[:size] {
			team, found := ids[row.Team.ID]
			if !found {
				delete(groups, key)
				break
			}
			groups[key] = append(groups[key], team)
		}
	}
	maxShield := 0.0
	for _, row := range rows {
		maxShield = max(maxShield, row.ShieldProbability)
	}
	if maxShield > 0 {
		for _, row := range rows {
			if row.ShieldProbability != maxShield {
				continue
			}
			team, found := ids[row.Team.ID]
			if !found {
				delete(groups, "shield")
				break
			}
			groups["shield"] = append(groups["shield"], team)
		}
	}
	return groups
}

func exploreTrendBestTeams(teams []exploreTeamRecord, measure int, expected bool, teamCount int) []exploreTeamRecord {
	if len(teams) == 0 || len(teams) != teamCount {
		return nil
	}
	var best *float64
	var holders []exploreTeamRecord
	for _, team := range teams {
		mean := exploreTrendMeanFor([]exploreTeamRecord{team}, measure, expected)
		if mean.Value == nil || mean.Count != mean.Total {
			return nil
		}
		value := *mean.Value
		if best == nil || (measure == 1 && value < *best) || (measure != 1 && value > *best) {
			best, holders = &value, []exploreTeamRecord{team}
		} else if value == *best {
			holders = append(holders, team)
		}
	}
	return holders
}

func exploreTrendReferenceOptions(season exploreTeamSeason) []exploreTrendReferenceOption {
	options := []exploreTrendReferenceOption{{Key: "team", Label: "None (team average only)"}}
	for _, benchmark := range season.Benchmarks {
		options = append(options, exploreTrendReferenceOption{Key: benchmark.Key, Label: benchmark.Label})
	}
	return options
}

func exploreTrendReferenceViews(season exploreTeamSeason, team exploreTeamRecord, columns []exploreTrendColumn, reference string) []exploreTrendReferenceView {
	var rows []exploreTrendReferenceView
	for _, column := range columns {
		own := exploreTrendMeanFor([]exploreTeamRecord{team}, column.Measure, column.Expected)
		rows = append(rows, exploreTrendReferenceDisplay(column.Label+" · Team average", own, "All recorded matches for this team; expected means use available observations."))
		for _, benchmark := range season.Benchmarks {
			if benchmark.Key != reference {
				continue
			}
			basis := 0
			if column.Expected {
				basis = 1
			}
			rows = append(rows, exploreTrendReferenceDisplay(column.Label+" · "+benchmark.Label, benchmark.Means[column.Measure][basis], benchmark.Note))
		}
	}
	return rows
}

func exploreTrendReferenceDisplay(label string, mean exploreTrendMean, note string) exploreTrendReferenceView {
	return exploreTrendReferenceView{Label: label, Value: exploreTrendNumber(mean.Value), Coverage: fmt.Sprintf("%d of %d team match appearances", mean.Count, mean.Total), Members: strings.Join(mean.Names, ", "), Note: note}
}

func exploreTrendUnavailableReference(season exploreTeamSeason, columns []exploreTrendColumn, reference string) string {
	for _, benchmark := range season.Benchmarks {
		if benchmark.Key != reference {
			continue
		}
		var missing []string
		for _, column := range columns {
			basis := 0
			if column.Expected {
				basis = 1
			}
			if benchmark.Means[column.Measure][basis].Value == nil {
				missing = append(missing, column.Label)
			}
		}
		if len(missing) > 0 {
			return benchmark.Label + " unavailable for " + strings.Join(missing, ", ") + ". " + benchmark.Note
		}
	}
	return ""
}
