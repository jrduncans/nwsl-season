package history

import (
	"fmt"
	"slices"
	"strings"

	"github.com/jrduncans/nwsl-season/internal/bracket"
	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

// ThresholdFinish separates the cached match record from the official record,
// which includes awarded matches and disciplinary deductions.
type ThresholdFinish struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Place          int    `json:"place"`
	Played         int    `json:"played"`
	Earned         int    `json:"earned"`
	OfficialPlayed int    `json:"officialPlayed"`
	OfficialPoints int    `json:"officialPoints"`
}

type Threshold struct {
	Label      string          `json:"label"`
	Cutoff     ThresholdFinish `json:"cutoff"`
	Next       ThresholdFinish `json:"next"`
	PPG        *float64        `json:"ppg"`
	NextPPG    *float64        `json:"nextPPG"`
	Reason     string          `json:"reason"`
	Applicable bool            `json:"applicable"`
	Tied       bool            `json:"tied"`
}

type SeasonThresholds struct {
	Season                    string
	Playoffs, Home, Bye       int
	Round                     string
	Note                      string
	OrderSource, FormatSource string
	Values                    []Threshold
}

// Thresholds derives outcomes from the coherent cache snapshot and existing
// catalog topology. Sparse overrides supply only facts missing from ASA.
// Season-specific standings rules resolve ties from the cached match results.
func Thresholds(summaries []SeasonScoring, archive []cache.HistoricalSeason) []SeasonThresholds {
	bySeason := map[string]SeasonScoring{}
	snapshots := map[string]cache.HistoricalSeason{}
	entries := map[string]competition.Entry{}
	for _, entry := range competition.PublicEntries() {
		if entry.Stage == "Regular Season" {
			entries[entry.Season] = entry
		}
	}
	for _, summary := range summaries {
		bySeason[summary.Season] = summary
	}
	for _, snapshot := range archive {
		snapshots[snapshot.Entry.Season] = snapshot
		entries[snapshot.Entry.Season] = snapshot.Entry
	}
	overrides := competition.HistoricalOutcomeOverrides()
	var rows []SeasonThresholds
	for year, entry := range entries {
		summary := bySeason[year]
		playoffs, found := competition.Lookup(year, "Playoffs")
		if !found || playoffs.BracketFormat == nil {
			continue
		}
		// An active season enters when verified regular-season inventory and a
		// complete postseason prove closure, even before the calendar rolls over.
		if entry.Inventory != nil && summary.Lifecycle != cache.SourceScopeCompleted {
			if !regularThresholdsComplete(summary) || bracket.Build(*playoffs.BracketFormat, snapshots[year].PlayoffGames).State != bracket.StateReady {
				continue
			}
		}
		row := deriveThresholdSeason(entry, summary, snapshots[year], *playoffs.BracketFormat, overrides[year])
		rows = append(rows, row)
	}
	slices.SortFunc(rows, func(a, b SeasonThresholds) int { return strings.Compare(a.Season, b.Season) })
	return rows
}

func regularThresholdsComplete(summary SeasonScoring) bool {
	return summary.Readiness == cache.SourceReadinessAvailable && summary.Inventory == cache.InventoryCompletenessComplete && summary.Pending == 0 && summary.InvalidCompleted == 0
}

func deriveThresholdSeason(entry competition.Entry, summary SeasonScoring, snapshot cache.HistoricalSeason, format competition.BracketFormat, override competition.OutcomeOverrides) SeasonThresholds {
	row := SeasonThresholds{Season: entry.Season, Playoffs: entry.PlayoffPlaces,
		OrderSource: "seasons/" + entry.Season + "/regular-season", FormatSource: "seasons/" + entry.Season + "/playoffs"}
	openingSeeds := []int{}
	for _, slot := range format.Slots {
		if slot.RoundID == format.Rounds[0].ID && slot.SeedPair != nil {
			openingSeeds = append(openingSeeds, slot.SeedPair[:]...)
			row.Home++
		}
	}
	if len(openingSeeds) > 0 {
		row.Bye = slices.Min(openingSeeds) - 1
	}
	row.Home += row.Bye
	row.Round = strings.TrimSuffix(format.Rounds[0].Label, "s")
	finishes, outcomeSeeds, reason := thresholdFinishes(summary, snapshot, format, row, override)
	for index, spec := range []struct {
		label string
		place int
	}{{"Shield", 1}, {"Home playoff game", row.Home}, {"Playoff bye", row.Bye}, {"Playoffs", row.Playoffs}} {
		value := Threshold{Label: spec.label, Applicable: spec.place > 0}
		switch {
		case !value.Applicable:
			value.Reason = "Not applicable"
		case reason != "":
			value.Reason = reason
		case outcomeSeeds[index].reason != "":
			value.Reason = outcomeSeeds[index].reason
		default:
			for id, seeds := range outcomeSeeds[index].candidates {
				if len(seeds) != 1 {
					continue
				}
				finish := finishes[id]
				finish.Place = seeds[0]
				if finish.Place == spec.place {
					value.Cutoff = finish
				}
				if finish.Place == spec.place+1 {
					value.Next = finish
				}
			}
			if value.Cutoff.ID == "" || value.Next.ID == "" {
				value.Reason = "Tied finishing order remains ambiguous; a source-backed exception is needed"
			} else {
				ppg := float64(value.Cutoff.OfficialPoints) / float64(value.Cutoff.OfficialPlayed)
				next := float64(value.Next.OfficialPoints) / float64(value.Next.OfficialPlayed)
				value.PPG, value.NextPPG = &ppg, &next
				value.Tied = value.Cutoff.OfficialPoints == value.Next.OfficialPoints
			}
		}
		row.Values = append(row.Values, value)
	}
	// Notes explain only the adjustments behind a published cutoff.
	shown := map[string]bool{}
	for _, value := range row.Values {
		if value.PPG != nil {
			shown[value.Cutoff.ID], shown[value.Next.ID] = true, true
		}
	}
	for _, award := range override.Awards {
		if shown[award.Winner] || shown[award.Loser] {
			row.Note = "Official points and games include awarded forfeits."
		}
	}
	for _, deduction := range override.Deductions {
		if shown[deduction.Team] {
			row.Note = strings.TrimSpace(row.Note + fmt.Sprintf(" %s: %d-point disciplinary deduction included in official points.", finishes[deduction.Team].Name, deduction.Points))
		}
	}
	return row
}

// thresholdSeeds is the seed evidence behind one outcome's boundary. A reason
// withholds only that outcome.
type thresholdSeeds struct {
	candidates map[string][]int
	reason     string
}

const tieOverrideConflict = "Tied-order exception conflicts with cached results"

// thresholdEvidence names the playoff facts a seed must agree with.
type thresholdEvidence struct{ qualified, hosts, byes bool }

// Each outcome keeps only the evidence about its own cutoff when the combined
// evidence conflicts. Shield depends on the regular-season order alone.
var thresholdOutcomeEvidence = [4]thresholdEvidence{{}, {hosts: true}, {byes: true}, {qualified: true}}

// thresholdFinishes returns each outcome's seed candidates in the order
// Shield, home game, bye, playoffs. The combined evidence is used while it is
// consistent. A season-level reason withholds every outcome.
func thresholdFinishes(summary SeasonScoring, snapshot cache.HistoricalSeason, format competition.BracketFormat, season SeasonThresholds, override competition.OutcomeOverrides) (map[string]ThresholdFinish, [4]thresholdSeeds, string) {
	finishes := map[string]ThresholdFinish{}
	var none [4]thresholdSeeds
	if summary.Readiness != cache.SourceReadinessAvailable || len(summary.Teams) <= season.Playoffs {
		return finishes, none, "Regular-season records not loaded"
	}
	if summary.Lifecycle != cache.SourceScopeCompleted && !regularThresholdsComplete(summary) {
		return finishes, none, "Regular season is not completed"
	}
	if summary.Basis != AllShots || summary.Pending > 0 || summary.InvalidCompleted > 0 || summary.Inventory == cache.InventoryCompletenessIncomplete {
		return finishes, none, "Regular-season results are incomplete or invalid"
	}
	for _, team := range summary.Teams {
		finishes[team.TeamID] = ThresholdFinish{ID: team.TeamID, Name: team.TeamID, Played: team.Played, Earned: team.Points, OfficialPlayed: team.Played, OfficialPoints: team.Points}
	}
	for _, team := range snapshot.Data.Teams {
		if finish, found := finishes[team.ID]; found {
			finish.Name = standings.DisplayName(team)
			finishes[team.ID] = finish
		}
	}
	if reason := applyThresholdAdjustments(finishes, override); reason != "" {
		return finishes, none, reason
	}
	view := bracket.Build(format, snapshot.PlayoffGames)
	if view.State != bracket.StateReady {
		return finishes, none, "Complete, resolved playoff fixtures are required"
	}
	for _, game := range snapshot.PlayoffGames {
		if game.Status != fixtures.CompletedStatus {
			return finishes, none, "Playoffs are not completed"
		}
		if _, found := finishes[game.HomeTeamID]; !found {
			return finishes, none, "Playoff participants do not match regular-season records"
		}
		if _, found := finishes[game.AwayTeamID]; !found {
			return finishes, none, "Playoff participants do not match regular-season records"
		}
	}
	ranked := thresholdRanking(snapshot, finishes, override)
	combined := thresholdSeedCandidates(ranked, view, season, thresholdEvidence{true, true, true})
	reason := resolveThresholdSeeds(combined, finishes, view, format, override.Ties, true)
	if reason == tieOverrideConflict {
		return finishes, none, reason
	}
	var seeds [4]thresholdSeeds
	for index, evidence := range thresholdOutcomeEvidence {
		if reason == "" {
			seeds[index].candidates = combined
			continue
		}
		own := thresholdSeedCandidates(ranked, view, season, evidence)
		seeds[index] = thresholdSeeds{own, resolveThresholdSeeds(own, finishes, view, format, override.Ties, false)}
	}
	return finishes, seeds, ""
}

// Awards fill observed game-count deficits only. If ASA later supplies the
// award, its game and points are already counted and the override is skipped.
// An asymmetric or unexplained deficit withholds the season rather than
// silently adding a match twice or treating missing results as forfeits.
func applyThresholdAdjustments(finishes map[string]ThresholdFinish, override competition.OutcomeOverrides) string {
	expected := 0
	for _, finish := range finishes {
		expected = max(expected, finish.Played)
	}
	for _, award := range override.Awards {
		winner, winOK := finishes[award.Winner]
		loser, loseOK := finishes[award.Loser]
		if !winOK || !loseOK {
			return "Awarded-match exception does not match cached participants"
		}
		if winner.OfficialPlayed == expected {
			continue
		}
		if winner.OfficialPlayed >= expected || loser.OfficialPlayed >= expected {
			return "Awarded-match records do not reconcile"
		}
		winner.OfficialPlayed++
		winner.OfficialPoints += 3
		loser.OfficialPlayed++
		finishes[award.Winner], finishes[award.Loser] = winner, loser
	}
	for _, finish := range finishes {
		if finish.OfficialPlayed == 0 || finish.OfficialPlayed != expected {
			return "Regular-season game counts do not reconcile after awarded matches"
		}
	}
	for _, deduction := range override.Deductions {
		finish, found := finishes[deduction.Team]
		if !found || finish.OfficialPoints < deduction.Points {
			return "Point-deduction exception does not match cached records"
		}
		finish.OfficialPoints -= deduction.Points
		finishes[deduction.Team] = finish
	}
	return ""
}

// Awards are represented as scored fixtures as well as official points, so
// head-to-head, wins, goal difference and venue criteria include the same facts.
func thresholdRanking(snapshot cache.HistoricalSeason, finishes map[string]ThresholdFinish, override competition.OutcomeOverrides) []standings.TableRow {
	games := make([]standings.Game, 0, len(snapshot.Data.Games)+len(override.Awards))
	for _, cached := range snapshot.Data.Games {
		game := standings.Game{ID: cached.ASAID, Status: cached.Status, HomeTeamID: cached.HomeTeamID, AwayTeamID: cached.AwayTeamID}
		if cached.HomeScore.Valid {
			value := int(cached.HomeScore.Int64)
			game.HomeScore = &value
		}
		if cached.AwayScore.Valid {
			value := int(cached.AwayScore.Int64)
			game.AwayScore = &value
		}
		games = append(games, game)
	}
	// Reconcile in the same order as applyThresholdAdjustments, including when
	// ASA already supplies one or both awards.
	played := map[string]int{}
	for id, finish := range finishes {
		played[id] = finish.Played
	}
	for _, award := range override.Awards {
		if played[award.Winner] >= finishes[award.Winner].OfficialPlayed {
			continue
		}
		home, away := award.Winner, award.Loser
		homeScore, awayScore := award.WinnerGoals, award.LoserGoals
		if award.Home == award.Loser {
			home, away = away, home
			homeScore, awayScore = awayScore, homeScore
		}
		games = append(games, standings.Game{Status: standings.CompletedStatus, HomeTeamID: home, AwayTeamID: away, HomeScore: &homeScore, AwayScore: &awayScore})
		played[award.Winner]++
		played[award.Loser]++
	}
	rules := standings.RulesForSeason(snapshot.Entry.Season, standings.OrderOfficialTotal)
	rows := standings.Calculate(snapshot.Data.Teams, games, rules)
	for i := range rows {
		rows[i].Record.Points = finishes[rows[i].Team.ID].OfficialPoints
	}
	return standings.Rank(rows, games, rules)
}

// Season-specific rules determine ranks. Unavailable final tiebreakers leave
// intervals that postseason evidence can constrain. The neutral final supplies no hosts.
func thresholdSeedCandidates(ranked []standings.TableRow, view bracket.View, season SeasonThresholds, evidence thresholdEvidence) map[string][]int {
	qualified, hosts, byes := map[string]bool{}, map[string]bool{}, map[string]bool{}
	seen := map[string]bool{}
	for roundIndex, round := range view.Rounds {
		if round.ID == "final" {
			continue
		}
		for _, slot := range round.Slots {
			for _, id := range []string{slot.Home.TeamID, slot.Away.TeamID} {
				qualified[id] = true
				if !seen[id] {
					hosts[id] = id == slot.Home.TeamID
					byes[id] = roundIndex > 0
					seen[id] = true
				}
			}
		}
	}
	candidates := map[string][]int{}
	for index, row := range ranked {
		id := row.Team.ID
		candidates[id] = nil
		first, last := index+1, index+1
		if row.TieBreak.Undetermined {
			for otherIndex, other := range ranked {
				if slices.Contains(row.TieBreak.TiedTeamIDs, other.Team.ID) {
					first = min(first, otherIndex+1)
					last = max(last, otherIndex+1)
				}
			}
		}
		for seed := first; seed <= last; seed++ {
			if (!evidence.qualified || (seed <= season.Playoffs) == qualified[id]) && (!evidence.hosts || (seed <= season.Home) == hosts[id]) && (!evidence.byes || (seed <= season.Bye) == byes[id]) {
				candidates[id] = append(candidates[id], seed)
			}
		}
	}
	return candidates
}

// resolveThresholdSeeds narrows candidates with tie exceptions and unique
// seeds. With pairing, it also uses the opening-round matchups and rejects any
// team without a consistent seed.
func resolveThresholdSeeds(candidates map[string][]int, finishes map[string]ThresholdFinish, view bracket.View, format competition.BracketFormat, ties []competition.OutcomeTie, pairing bool) string {
	for _, tie := range ties {
		first := len(finishes) + 1
		for _, id := range tie.Order {
			if len(candidates[id]) < 2 {
				return tieOverrideConflict
			}
			first = min(first, slices.Min(candidates[id]))
		}
		points := finishes[tie.Order[0]].OfficialPoints
		for index, id := range tie.Order {
			seed := first + index
			if finishes[id].OfficialPoints != points || !slices.Contains(candidates[id], seed) {
				return tieOverrideConflict
			}
			candidates[id] = []int{seed}
		}
	}
	for changed := true; changed; {
		changed = false
		for id, seeds := range candidates {
			if len(seeds) != 1 {
				continue
			}
			for other, remaining := range candidates {
				if other == id {
					continue
				}
				filtered := slices.DeleteFunc(slices.Clone(remaining), func(seed int) bool { return seed == seeds[0] })
				if len(filtered) != len(remaining) {
					candidates[other] = filtered
					changed = true
				}
			}
		}
		openingSlots := view.Rounds[0].Slots
		if !pairing {
			openingSlots = nil
		}
		for _, slot := range openingSlots {
			pairs := [][2]int{}
			for _, spec := range format.Slots {
				if spec.RoundID != view.Rounds[0].ID || spec.SeedPair == nil || (spec.SourceOrder > 0 && spec.ID != slot.ID) {
					continue
				}
				pair := *spec.SeedPair
				if slices.Contains(candidates[slot.Home.TeamID], pair[0]) && slices.Contains(candidates[slot.Away.TeamID], pair[1]) {
					pairs = append(pairs, pair)
				}
			}
			if len(pairs) == 0 {
				return "Playoff seeds conflict with regular-season points or hosting evidence"
			}
			if len(pairs) == 1 {
				for index, id := range []string{slot.Home.TeamID, slot.Away.TeamID} {
					if len(candidates[id]) != 1 {
						candidates[id] = []int{pairs[0][index]}
						changed = true
					}
				}
			}
		}
	}
	for id, seeds := range candidates {
		if len(seeds) == 0 {
			return fmt.Sprintf("%s: playoff evidence and regular-season points disagree", finishes[id].Name)
		}
	}
	return ""
}
