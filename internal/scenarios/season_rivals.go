package scenarios

import (
	"fmt"
	"slices"
	"sort"
)

// RivalSummary restates a rest-of-season result one rival at a time. Once the
// team's own results are fixed, whether it finishes ahead of one rival
// depends only on the two teams' results and scores, unless head-to-head among
// three or more level teams decides it. A summary is published only when that
// cannot matter, so it describes exactly the same scorelines as the clauses.
type RivalSummary struct {
	Rows []RivalRow `json:"rows"`
}

// RivalRow is one combination of the team's own results that can clinch. The
// team clinches when it finishes ahead of at least Need of Rivals; every other
// team finishes ahead of it, or behind it, whatever happens. A row that
// clinches outright has Need 0 and no rivals.
//
// A Withheld row is one where head-to-head among three or more level teams
// could decide, so no pairwise statement is exact: the clauses for those own
// results describe it instead. It has Need 0 and no rivals, and may turn out
// to have no clinching clause.
type RivalRow struct {
	Own      []FixtureCondition `json:"own"`
	Need     int                `json:"need"`
	Rivals   []RivalNeed        `json:"rivals"`
	Withheld bool               `json:"withheld,omitempty"`
}

// RivalNeed says when the team finishes ahead of one rival: the rival takes
// at most MaxPoints from GameIDs, its remaining matches other than against
// the team, or it takes one of the Ties records and that tie's condition
// holds. MaxPoints is -1 when no points total is enough by itself.
type RivalNeed struct {
	RivalID   string     `json:"rival_id"`
	GameIDs   []string   `json:"game_ids"`
	MaxPoints int        `json:"max_points"`
	Ties      []RivalTie `json:"ties,omitempty"`
}

// RivalTie is a record from GameIDs that leaves the rival level with the team
// on points. A nil Condition means the team finishes ahead whatever the
// scores; otherwise the condition decides it.
type RivalTie struct {
	Points    int             `json:"points"`
	Wins      int             `json:"wins"`
	Condition *ScoreCondition `json:"condition,omitempty"`
}

// HasWithheld reports whether any row defers to the clauses.
func (r RivalSummary) HasWithheld() bool {
	for _, row := range r.Rows {
		if row.Withheld {
			return true
		}
	}
	return false
}

// Bounds on the per-rival restatement. Larger slates keep only the clauses.
const (
	maxRivalOwnMatches   = 4
	maxRivalOtherMatches = 6
)

// rivalRow is one row of own results before a threshold is applied.
type rivalRow struct {
	own []FixtureCondition
	// ahead counts teams that finish ahead of the target whatever happens.
	ahead  int
	rivals []RivalNeed
	// levelRivals counts rivals that could finish level with the target
	// through goals scored. With two or more, head-to-head among three teams
	// could decide, so their pairwise verdicts may be wrong: levelAhead of
	// them are counted in ahead, and levelBehind were left out as finishing
	// behind the target.
	levelRivals, levelAhead, levelBehind int
}

// rivalRows fixes each combination of the target's own results and compares
// it with every other team over that team's remaining results. It must run
// with no outcome fixed, and leaves none fixed.
func (s *seasonSearch) rivalRows() ([]rivalRow, bool) {
	own := s.teamGames[s.target]
	if len(own) > maxRivalOwnMatches {
		return nil, false
	}
	rows := []rivalRow{}
	ok := true
	var visit func(int)
	visit = func(i int) {
		if !ok {
			return
		}
		if i == len(own) {
			row, rowOK := s.rivalRow(own)
			rows = append(rows, row)
			ok = rowOK
			return
		}
		for outcome := int8(0); outcome < 3; outcome++ {
			s.set(own[i], outcome)
			visit(i + 1)
			s.clear(own[i])
		}
	}
	visit(0)
	return rows, ok
}

func (s *seasonSearch) rivalRow(own []int) (rivalRow, bool) {
	t := s.target
	row := rivalRow{}
	for _, index := range own {
		row.own = append(row.own, FixtureCondition{GameID: s.games[index].id, AllowedOutcomes: maskOutcomes(outcomeMasks[s.outcome[index]])})
	}
	sort.Slice(row.own, func(i, j int) bool { return row.own[i].GameID < row.own[j].GameID })
	for rival := range s.teamIDs {
		if rival == t {
			continue
		}
		free := []int{}
		for _, index := range s.teamGames[rival] {
			if s.outcome[index] < 0 {
				free = append(free, index)
			}
		}
		// tie is the points from its other matches that leave the rival level.
		tie := s.points[t] - s.points[rival]
		switch {
		case tie > 3*len(free):
			continue
		case tie < 0:
			row.ahead++
			continue
		case len(free) > maxRivalOtherMatches:
			return row, false
		}
		need, level, ok := s.rivalNeed(rival, free, tie)
		if !ok {
			return row, false
		}
		if level {
			row.levelRivals++
		}
		switch {
		case need.MaxPoints >= 3*len(free):
			if level {
				row.levelBehind++
			}
		case need.MaxPoints < 0 && len(need.Ties) == 0:
			row.ahead++
			if level {
				row.levelAhead++
			}
		default:
			row.rivals = append(row.rivals, need)
		}
	}
	return row, true
}

// rivalNeed walks the rival's remaining matches and collects the records that
// leave it level on points. level reports whether any of them can leave the
// two level through goals scored, so head-to-head could decide.
func (s *seasonSearch) rivalNeed(rival int, free []int, tie int) (RivalNeed, bool, bool) {
	need := RivalNeed{RivalID: s.teamID(rival), GameIDs: []string{}}
	for _, index := range free {
		need.GameIDs = append(need.GameIDs, s.games[index].id)
	}
	type record struct{ points, wins int }
	verdicts := map[record]pairResult{}
	level, ok := false, true
	var visit func(int, record)
	visit = func(i int, r record) {
		if !ok {
			return
		}
		if i == len(free) {
			if r.points != tie {
				return
			}
			if _, isLevel := s.levelRegions(rival); isLevel {
				level = true
			}
			verdict := s.rivalVerdict(rival)
			if prior, seen := verdicts[r]; seen && (prior.verdict != verdict.verdict || scoreConditionKey(prior.condition) != scoreConditionKey(verdict.condition)) {
				// The same record must always give the same verdict.
				ok = false
			}
			verdicts[r] = verdict
			return
		}
		game := s.games[free[i]]
		for outcome := int8(0); outcome < 3; outcome++ {
			next := r
			switch teamResult(game, outcome, rival) {
			case 1:
				next.points, next.wins = r.points+3, r.wins+1
			case 0:
				next.points++
			}
			s.set(free[i], outcome)
			visit(i+1, next)
			s.clear(free[i])
		}
	}
	visit(0, record{})
	if !ok {
		return need, level, false
	}
	allAhead := true
	records := make([]record, 0, len(verdicts))
	for r, verdict := range verdicts {
		records = append(records, r)
		allAhead = allAhead && verdict.verdict == pairAhead
	}
	if allAhead {
		need.MaxPoints = tie
		return need, level, true
	}
	need.MaxPoints = tie - 1
	sort.Slice(records, func(i, j int) bool { return records[i].wins > records[j].wins })
	for _, r := range records {
		switch verdict := verdicts[r]; verdict.verdict {
		case pairAhead:
			need.Ties = append(need.Ties, RivalTie{Points: r.points, Wins: r.wins})
		case pairConditional:
			condition := verdict.condition
			need.Ties = append(need.Ties, RivalTie{Points: r.points, Wins: r.wins, Condition: &condition})
		}
	}
	return need, level, true
}

// rivalVerdict compares the target with a rival level with it on points,
// with both teams' results fixed. A rival that can finish level through goals
// scored forms a head-to-head group with the target alone; rivalRow rules
// out rows where a second rival could join it.
func (s *seasonSearch) rivalVerdict(rival int) pairResult {
	t := s.target
	lr, ok := s.levelRegions(rival)
	if !ok {
		return s.pair(rival)
	}
	ahead := func(levelWins bool) pairResult {
		switch {
		case levelWins && !lr.possible[stateBehind]:
			return pairResult{verdict: pairAhead}
		case levelWins:
			return pairResult{verdict: pairConditional, condition: lr.aheadOrLevel[0]}
		case !lr.possible[stateAhead]:
			return pairResult{verdict: pairBehind}
		}
		return pairResult{verdict: pairConditional, condition: lr.token[stateAhead][0]}
	}
	switch points := s.headToHeadPoints(t, rival) - s.headToHeadPoints(rival, t); {
	case points != 0:
		return ahead(points > 0)
	case !lr.h2hFromRemaining:
		// A level finish with level head-to-head goals needs disciplinary
		// points, which are never claimed.
		return ahead(lr.h2hNow > 0)
	}
	// Their remaining matches decide head-to-head goals: the target finishes
	// ahead before head-to-head, or level and with more head-to-head goals.
	condition := lr.token[stateAhead][0]
	condition.Levels = append(slices.Clone(condition.Levels), s.level(t, rival, MeasureHeadToHeadGoals, -lr.h2hNow+1, false))
	return pairResult{verdict: pairConditional, condition: condition}
}

// rivalSummary applies a top-K threshold to the rows. Where head-to-head
// among three teams could decide, a pairwise verdict could be wrong either
// way, so such a row is settled only when it clinches even if every rival in
// doubt finishes ahead, or cannot clinch even if every rival that could be
// level finishes behind; otherwise it is withheld. A summary with every row
// withheld says nothing, so it is nil.
func rivalSummary(rows []rivalRow, topK int) *RivalSummary {
	summary := &RivalSummary{Rows: []RivalRow{}}
	slots := topK - 1
	stated := false
	for _, row := range rows {
		if row.levelRivals > 1 {
			switch {
			case row.ahead-row.levelAhead > slots:
			case row.ahead+len(row.rivals)+row.levelBehind <= slots:
				summary.Rows = append(summary.Rows, RivalRow{Own: row.own, Rivals: []RivalNeed{}})
				stated = true
			default:
				summary.Rows = append(summary.Rows, RivalRow{Own: row.own, Rivals: []RivalNeed{}, Withheld: true})
			}
			continue
		}
		stated = stated || len(row.rivals)+row.ahead-slots <= len(row.rivals)
		switch need := len(row.rivals) + row.ahead - slots; {
		case need > len(row.rivals):
		case need <= 0:
			summary.Rows = append(summary.Rows, RivalRow{Own: row.own, Rivals: []RivalNeed{}})
		default:
			summary.Rows = append(summary.Rows, RivalRow{Own: row.own, Need: need, Rivals: row.rivals})
		}
	}
	if !stated {
		return nil
	}
	return summary
}

// validate checks a summary against the slate and the team it describes.
func (r RivalSummary) validate(team string, fixture map[string]bool) error {
	if len(r.Rows) == 0 {
		return fmt.Errorf("invalid rival summary: no rows")
	}
	for _, row := range r.Rows {
		if err := validateConditions(row.Own, fixture); err != nil {
			return err
		}
		for _, own := range row.Own {
			if len(own.AllowedOutcomes) != 1 {
				return fmt.Errorf("invalid rival summary: own result is not a single outcome")
			}
		}
		if row.Rivals == nil || row.Need < 0 || row.Need > len(row.Rivals) || (row.Need == 0) != (len(row.Rivals) == 0) || (row.Withheld && row.Need != 0) {
			return fmt.Errorf("invalid rival summary: invalid need")
		}
		seen := map[string]bool{}
		for _, rival := range row.Rivals {
			if rival.RivalID == "" || rival.RivalID == team || seen[rival.RivalID] || rival.GameIDs == nil || rival.MaxPoints < -1 {
				return fmt.Errorf("invalid rival summary: invalid rival")
			}
			seen[rival.RivalID] = true
			for _, id := range rival.GameIDs {
				if !fixture[id] {
					return fmt.Errorf("invalid rival summary: unknown rival fixture")
				}
			}
			for _, tie := range rival.Ties {
				if tie.Points != rival.MaxPoints+1 || tie.Wins < 0 {
					return fmt.Errorf("invalid rival summary: invalid tie")
				}
				if tie.Condition == nil {
					continue
				}
				if tie.Condition.TeamID != team || tie.Condition.RivalID != rival.RivalID {
					return fmt.Errorf("invalid rival summary: tie condition names other teams")
				}
				if err := validateScoreConditions([]ScoreCondition{*tie.Condition}, team); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
