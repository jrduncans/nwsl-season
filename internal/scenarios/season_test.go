package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/clinching"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

// seasonFixture is a small league with a few remaining matches.
type seasonFixture struct {
	teams   []standings.Team
	games   []standings.Game
	pending []int // indices into games
}

func newSeasonFixture(teams int) *seasonFixture {
	f := &seasonFixture{}
	for i := range teams {
		id := string(rune('A' + i))
		f.teams = append(f.teams, standings.Team{ID: id, Name: id})
	}
	return f
}

func (f *seasonFixture) played(home, away string, homeScore, awayScore int) {
	h, a := homeScore, awayScore
	f.games = append(f.games, standings.Game{ID: fmt.Sprintf("g%d", len(f.games)), Status: standings.CompletedStatus, HomeTeamID: home, AwayTeamID: away, HomeScore: &h, AwayScore: &a})
}

func (f *seasonFixture) upcoming(home, away string) {
	f.pending = append(f.pending, len(f.games))
	f.games = append(f.games, standings.Game{ID: fmt.Sprintf("g%d", len(f.games)), Status: fixtures.PreMatchStatus, HomeTeamID: home, AwayTeamID: away})
}

func (f *seasonFixture) slate() Slate {
	s := Slate{ID: "season", DefinitionVersion: SeasonDefinitionVersion, State: SlateReady, Source: SourceRestOfSeason, FixtureIDs: []string{}, StartsAtUTC: time.Unix(0, 0), LatestKickoffUTC: time.Unix(0, 0), CutoffUTC: time.Unix(0, 0)}
	for _, index := range f.pending {
		s.FixtureIDs = append(s.FixtureIDs, f.games[index].ID)
	}
	return s
}

func (f *seasonFixture) generate(t *testing.T, team string, achievements []competition.Achievement) map[competition.AchievementID]Result {
	t.Helper()
	baselines := map[competition.AchievementID]clinching.AchievementResult{}
	for _, achievement := range achievements {
		baselines[achievement.ID] = clinching.AchievementResult{TeamID: team, Achievement: achievement.ID, TopK: achievement.TopK, Status: clinching.NotClinched, Method: clinching.ProofPointsOptimization}
	}
	slate := f.slate()
	results, err := GenerateSeason(context.Background(), SeasonRequest{Teams: f.teams, Games: f.games, Slate: slate, TargetTeamID: team, Achievements: achievements, Baselines: baselines})
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range results {
		if err := result.Validate(slate); err != nil {
			t.Fatalf("%s %s: %v", team, result.Achievement, err)
		}
	}
	return results
}

// placement is the official outcome for one completed scoreline: each team's
// best and worst possible table index, which differ only for a tie that
// needs disciplinary points.
type placement struct{ best, worst map[string]int }

// eachScoreline completes the remaining matches with every scoreline whose
// goals are at most maxGoals per side.
func (f *seasonFixture) eachScoreline(maxGoals int, visit func(scores [][2]int, place placement)) {
	scores := make([][2]int, len(f.pending))
	games := append([]standings.Game(nil), f.games...)
	var walk func(int)
	walk = func(i int) {
		if i == len(f.pending) {
			for j, index := range f.pending {
				h, a := scores[j][0], scores[j][1]
				games[index].Status, games[index].HomeScore, games[index].AwayScore = standings.CompletedStatus, &h, &a
			}
			table := standings.Calculate(f.teams, games, standings.OfficialTotalRules())
			place := placement{best: map[string]int{}, worst: map[string]int{}}
			for position, row := range table {
				place.best[row.Team.ID], place.worst[row.Team.ID] = position, position
			}
			for position, row := range table {
				if !row.TieBreak.Undetermined {
					continue
				}
				for _, id := range row.TieBreak.TiedTeamIDs {
					place.best[id] = min(place.best[id], position)
					place.worst[id] = max(place.worst[id], position)
				}
			}
			visit(scores, place)
			return
		}
		for h := 0; h <= maxGoals; h++ {
			for a := 0; a <= maxGoals; a++ {
				scores[i] = [2]int{h, a}
				walk(i + 1)
			}
		}
	}
	walk(0)
}

// covers reports whether a clause describes a completed scoreline.
func (f *seasonFixture) covers(clause Clause, scores [][2]int) bool {
	slot := map[string]int{}
	for j, index := range f.pending {
		slot[f.games[index].ID] = j
	}
	for _, condition := range clause.Conditions {
		score := scores[slot[condition.GameID]]
		outcome := scoreOutcome(score[0], score[1])
		allowed := false
		for _, o := range condition.AllowedOutcomes {
			allowed = allowed || o == outcome
		}
		if !allowed {
			return false
		}
	}
	for _, condition := range clause.ScoreConditions {
		gains := make([]int, len(condition.Levels))
		for i, level := range condition.Levels {
			gains[i] = f.gain(level.Measure, condition.TeamID, condition.RivalID, scores)
		}
		if !condition.Holds(gains) {
			return false
		}
	}
	return true
}

// gain measures a score condition directly from the remaining matches.
func (f *seasonFixture) gain(measure ScoreMeasure, team, rival string, scores [][2]int) int {
	value := 0
	for j, index := range f.pending {
		game := f.games[index]
		for _, side := range []struct {
			id   string
			sign int
		}{{team, 1}, {rival, -1}} {
			var own, other int
			switch side.id {
			case game.HomeTeamID:
				own, other = scores[j][0], scores[j][1]
			case game.AwayTeamID:
				own, other = scores[j][1], scores[j][0]
			default:
				continue
			}
			headToHead := (game.HomeTeamID == team || game.AwayTeamID == team) && (game.HomeTeamID == rival || game.AwayTeamID == rival)
			switch measure {
			case MeasureGoalDifference:
				value += side.sign * (own - other)
			case MeasureGoalsScored:
				value += side.sign * own
			case MeasureHeadToHeadGoals:
				if headToHead {
					value += side.sign * own
				}
			}
		}
	}
	return value
}

// rivalsClinch reports whether a rival summary describes a completed
// scoreline: its row of own results, then at least Need rivals the team
// finishes ahead of. A withheld row defers to the clauses, which withheld
// reports.
func (f *seasonFixture) rivalsClinch(result Result, scores [][2]int) (clinch, withheld bool) {
	slot := map[string]int{}
	for j, index := range f.pending {
		slot[f.games[index].ID] = j
	}
	for _, row := range result.Rivals.Rows {
		if !f.covers(Clause{Conditions: row.Own}, scores) {
			continue
		}
		if row.Withheld {
			for _, clause := range result.Clauses {
				if f.covers(clause, scores) {
					return true, true
				}
			}
			return false, true
		}
		ahead := 0
		for _, rival := range row.Rivals {
			points, wins := 0, 0
			for _, id := range rival.GameIDs {
				game := f.games[f.pending[slot[id]]]
				own, other := scores[slot[id]][0], scores[slot[id]][1]
				if game.AwayTeamID == rival.RivalID {
					own, other = other, own
				}
				switch {
				case own > other:
					points, wins = points+3, wins+1
				case own == other:
					points++
				}
			}
			if points <= rival.MaxPoints {
				ahead++
				continue
			}
			for _, tie := range rival.Ties {
				if tie.Points != points || tie.Wins != wins {
					continue
				}
				if tie.Condition == nil || f.covers(Clause{ScoreConditions: []ScoreCondition{*tie.Condition}}, scores) {
					ahead++
				}
			}
		}
		return ahead >= row.Need, false
	}
	return false, false
}

// checkSeasonExhaustively compares every published clause with the official
// table for every scoreline up to maxGoals: a clause must only describe
// scorelines where the team definitely finishes in the top K, and when no
// limitation is stated, every such scoreline must be described. A rival
// summary must describe exactly those scorelines. It returns the number of
// rival summaries checked.
func checkSeasonExhaustively(t *testing.T, f *seasonFixture, maxGoals int) int {
	t.Helper()
	achievements := []competition.Achievement{}
	for k := 1; k < len(f.teams); k++ {
		achievements = append(achievements, competition.Achievement{ID: competition.AchievementID(fmt.Sprintf("top%d", k)), TopK: k})
	}
	results := map[string]map[competition.AchievementID]Result{}
	for _, team := range f.teams {
		results[team.ID] = f.generate(t, team.ID, achievements)
	}
	summaries := 0
	for _, team := range f.teams {
		for _, result := range results[team.ID] {
			if result.Rivals != nil {
				summaries++
			}
		}
	}
	disciplinarySeen := map[string]bool{}
	f.eachScoreline(maxGoals, func(scores [][2]int, place placement) {
		for _, team := range f.teams {
			for _, achievement := range achievements {
				result := results[team.ID][achievement.ID]
				definitely := place.worst[team.ID] < achievement.TopK
				crossing := place.best[team.ID] < achievement.TopK && !definitely
				covered := false
				for _, clause := range result.Clauses {
					if f.covers(clause, scores) {
						covered = true
						if !definitely {
							t.Fatalf("%s %s: clause %+v claims a clinch at %v, places %d-%d", team.ID, achievement.ID, clause, scores, place.best[team.ID], place.worst[team.ID])
						}
					}
				}
				if definitely && !covered && result.Limitation == "" {
					t.Fatalf("%s %s: clinch at %v is not described by %+v", team.ID, achievement.ID, scores, result.Clauses)
				}
				if result.Rivals != nil {
					// A withheld row defers to the clauses, which may be
					// incomplete when a limitation is stated.
					clinch, withheld := f.rivalsClinch(result, scores)
					if clinch != definitely && (clinch || !withheld || result.Limitation == "") {
						t.Fatalf("%s %s: rival summary %+v disagrees with places %d-%d at %v", team.ID, achievement.ID, result.Rivals, place.best[team.ID], place.worst[team.ID], scores)
					}
				}
				if crossing {
					disciplinarySeen[team.ID+string(achievement.ID)] = true
					if !result.DisciplinaryPossible && result.Limitation == "" {
						t.Fatalf("%s %s: disciplinary points can decide at %v without a note", team.ID, achievement.ID, scores)
					}
				}
			}
		}
	})
	return summaries
}

func randomSeasonFixture(random *rand.Rand, teams, remaining, maxScore int) *seasonFixture {
	f := newSeasonFixture(teams)
	for i := range f.teams {
		for j := i + 1; j < len(f.teams); j++ {
			home, away := f.teams[i].ID, f.teams[j].ID
			if random.Intn(2) == 0 {
				home, away = away, home
			}
			f.played(home, away, random.Intn(maxScore+1), random.Intn(maxScore+1))
		}
	}
	for range remaining {
		i := random.Intn(len(f.teams))
		j := (i + 1 + random.Intn(len(f.teams)-1)) % len(f.teams)
		f.upcoming(f.teams[i].ID, f.teams[j].ID)
	}
	return f
}

func TestSeasonScoreConditionsMatchTheOfficialTable(t *testing.T) {
	random := rand.New(rand.NewSource(20261024)) // #nosec G404 -- reproducible test leagues, not security randomness.
	summaries := 0
	for trial := range 150 {
		f := randomSeasonFixture(random, 5, 3, 2)
		t.Run(fmt.Sprint(trial), func(t *testing.T) { summaries += checkSeasonExhaustively(t, f, 3) })
	}
	if summaries < 100 {
		t.Fatalf("only %d rival summaries were checked", summaries)
	}
}

// Low scores and more teams make three-way ties, where head-to-head depends
// on which teams are still level.
func TestSeasonMultiwayTiesMatchTheOfficialTable(t *testing.T) {
	if testing.Short() {
		t.Skip("exhaustive scorelines")
	}
	random := rand.New(rand.NewSource(1101)) // #nosec G404 -- reproducible test leagues, not security randomness.
	for trial := range 40 {
		f := randomSeasonFixture(random, 6, 4, 1)
		t.Run(fmt.Sprint(trial), func(t *testing.T) { checkSeasonExhaustively(t, f, 2) })
	}
}

func TestSeasonGoalDifferenceThenGoalsScored(t *testing.T) {
	// A and B are level on points, wins, goal difference and goals scored,
	// and drew each other. If both win their last match, A needs a larger
	// goal-difference gain, or an equal one with more goals; fully level
	// leaves disciplinary points, which the result notes.
	f := newSeasonFixture(4)
	f.played("A", "C", 1, 0)
	f.played("B", "D", 1, 0)
	f.played("A", "B", 0, 0)
	f.played("C", "D", 0, 0)
	f.upcoming("A", "D")
	f.upcoming("B", "C")
	checkSeasonExhaustively(t, f, 4)
	result := f.generate(t, "A", []competition.Achievement{{ID: "top1", TopK: 1}})["top1"]
	if !result.DisciplinaryPossible {
		t.Fatal("expected a disciplinary-points note")
	}
	want := ScoreCondition{TeamID: "A", RivalID: "B", Levels: []ScoreLevel{
		{Measure: MeasureGoalDifference, MinimumGain: 1, TeamNow: 1, RivalNow: 1},
		{Measure: MeasureGoalsScored, MinimumGain: 1, TeamNow: 1, RivalNow: 1},
	}}
	for _, clause := range result.Clauses {
		if len(clause.ScoreConditions) == 1 && fmt.Sprint(clause.ScoreConditions[0]) == fmt.Sprint(want) && len(clause.Conditions) == 2 {
			return
		}
	}
	t.Fatalf("expected %+v when both win: %+v", want, result.Clauses)
}

func TestSeasonThreeWayHeadToHead(t *testing.T) {
	// B beat A, A beat C twice and C beat B. Level on everything through
	// goals scored, A leads a three-team group on head-to-head points (6 to
	// 3 and 3) but trails B alone (0 to 3), so the scenario must say which
	// teams finish level.
	f := newSeasonFixture(5)
	f.played("A", "B", 0, 1)
	f.played("A", "C", 1, 0)
	f.played("C", "A", 0, 1)
	f.played("B", "C", 0, 1)
	f.played("B", "D", 1, 0)
	f.played("C", "E", 1, 0)
	f.played("D", "E", 0, 0)
	f.upcoming("A", "D")
	f.upcoming("B", "E")
	f.upcoming("C", "D")
	checkSeasonExhaustively(t, f, 3)
	result := f.generate(t, "A", []competition.Achievement{{ID: "top1", TopK: 1}})["top1"]
	if result.Limitation != "" {
		t.Fatalf("unexpected limitation %q", result.Limitation)
	}
	for _, clause := range result.Clauses {
		level := 0
		for _, condition := range clause.ScoreConditions {
			if condition.Levels[len(condition.Levels)-1].ExactOnly {
				level++
			}
		}
		if level == 2 {
			return
		}
	}
	t.Fatalf("expected a scenario with A level with both B and C: %+v", result.Clauses)
}

func TestSeasonRivalSummaryStatesEachRival(t *testing.T) {
	// The fixture from TestSeasonGoalDifferenceThenGoalsScored. If A wins,
	// it clinches unless B wins too; then A needs the larger goal-difference
	// gain, or an equal one with more goals.
	f := newSeasonFixture(4)
	f.played("A", "C", 1, 0)
	f.played("B", "D", 1, 0)
	f.played("A", "B", 0, 0)
	f.played("C", "D", 0, 0)
	f.upcoming("A", "D")
	f.upcoming("B", "C")
	result := f.generate(t, "A", []competition.Achievement{{ID: "top1", TopK: 1}})["top1"]
	if result.Rivals == nil {
		t.Fatal("expected a rival summary")
	}
	want := RivalRow{
		Own:  []FixtureCondition{{GameID: "g4", AllowedOutcomes: []clinching.Outcome{clinching.HomeWin}}},
		Need: 1,
		Rivals: []RivalNeed{{RivalID: "B", GameIDs: []string{"g5"}, MaxPoints: 2, Ties: []RivalTie{{Points: 3, Wins: 1, Condition: &ScoreCondition{TeamID: "A", RivalID: "B", Levels: []ScoreLevel{
			{Measure: MeasureGoalDifference, MinimumGain: 1, TeamNow: 1, RivalNow: 1},
			{Measure: MeasureGoalsScored, MinimumGain: 1, TeamNow: 1, RivalNow: 1},
		}}}}}},
	}
	for _, row := range result.Rivals.Rows {
		if fmt.Sprint(row.Own) != fmt.Sprint(want.Own) {
			continue
		}
		got, _ := json.Marshal(row)
		expected, _ := json.Marshal(want)
		if string(got) != string(expected) {
			t.Fatalf("win row = %s, want %s", got, expected)
		}
		return
	}
	t.Fatalf("no row for an A win: %+v", result.Rivals.Rows)
}

func TestSeasonRivalSummaryWithholdsThreeWayHeadToHead(t *testing.T) {
	// The fixture from TestSeasonThreeWayHeadToHead: if A wins, B and C can
	// both finish level with it through goals scored, so head-to-head among
	// three teams can decide and that row defers to the clauses.
	f := newSeasonFixture(5)
	f.played("A", "B", 0, 1)
	f.played("A", "C", 1, 0)
	f.played("C", "A", 0, 1)
	f.played("B", "C", 0, 1)
	f.played("B", "D", 1, 0)
	f.played("C", "E", 1, 0)
	f.played("D", "E", 0, 0)
	f.upcoming("A", "D")
	f.upcoming("B", "E")
	f.upcoming("C", "D")
	result := f.generate(t, "A", []competition.Achievement{{ID: "top1", TopK: 1}})["top1"]
	if result.Rivals == nil {
		t.Fatal("expected a rival summary")
	}
	for _, row := range result.Rivals.Rows {
		if row.Own[0].AllowedOutcomes[0] == clinching.HomeWin {
			if !row.Withheld {
				t.Fatalf("expected the A-win row to be withheld: %+v", row)
			}
			return
		}
	}
	t.Fatalf("no row for an A win: %+v", result.Rivals.Rows)
}
