package cache

import (
	"database/sql"
	"errors"
)

// PenaltySplit is ASA's team-model penalty-pattern breakdown for one game.
type PenaltySplit struct {
	HomeXG, AwayXG       float64
	HomeGoals, AwayGoals int
	HomeShots, AwayShots int
}

// penaltyXGColumns lists the game_xg penalty columns in penaltyScan order.
const penaltyXGColumns = `home_penalty_xg,away_penalty_xg,home_penalty_goals,away_penalty_goals,home_penalty_shots,away_penalty_shots,penalty_raw_json`

// penaltyXGColumnsFor returns penaltyXGColumns qualified with a table alias.
func penaltyXGColumnsFor(alias string) string {
	return alias + `.home_penalty_xg,` + alias + `.away_penalty_xg,` + alias + `.home_penalty_goals,` + alias + `.away_penalty_goals,` + alias + `.home_penalty_shots,` + alias + `.away_penalty_shots,` + alias + `.penalty_raw_json`
}

// penaltyScan receives the nullable penalty columns of one game_xg row.
type penaltyScan struct {
	homeXG, awayXG       sql.NullFloat64
	homeGoals, awayGoals sql.NullInt64
	homeShots, awayShots sql.NullInt64
	raw                  string
}

func (p *penaltyScan) dests() []any {
	return []any{&p.homeXG, &p.awayXG, &p.homeGoals, &p.awayGoals, &p.homeShots, &p.awayShots, &p.raw}
}

// apply stores the scanned penalty columns on v. The columns are all-or-none.
func (p penaltyScan) apply(v *GameXG) error {
	valid := []bool{p.homeXG.Valid, p.awayXG.Valid, p.homeGoals.Valid, p.awayGoals.Valid, p.homeShots.Valid, p.awayShots.Valid}
	count := 0
	for _, ok := range valid {
		if ok {
			count++
		}
	}
	switch count {
	case 0:
		v.Penalties = nil
		v.PenaltyRawJSON = p.raw
		return nil
	case len(valid):
		v.Penalties = &PenaltySplit{
			HomeXG: p.homeXG.Float64, AwayXG: p.awayXG.Float64,
			HomeGoals: int(p.homeGoals.Int64), AwayGoals: int(p.awayGoals.Int64),
			HomeShots: int(p.homeShots.Int64), AwayShots: int(p.awayShots.Int64),
		}
		v.PenaltyRawJSON = p.raw
		return nil
	default:
		return errors.New("partial penalty columns")
	}
}

// penaltyArgs returns the values for the six nullable split columns followed
// by the raw JSON column.
func penaltyArgs(split *PenaltySplit, raw string) []any {
	if split == nil {
		return []any{nil, nil, nil, nil, nil, nil, ""}
	}
	return []any{split.HomeXG, split.AwayXG, split.HomeGoals, split.AwayGoals, split.HomeShots, split.AwayShots, raw}
}

func validPenaltySplit(s *PenaltySplit) bool {
	if s == nil {
		return true
	}
	return finiteNonnegative(s.HomeXG) && finiteNonnegative(s.AwayXG) &&
		s.HomeGoals >= 0 && s.AwayGoals >= 0 && s.HomeShots >= 0 && s.AwayShots >= 0
}

func samePenaltySplit(a, b *PenaltySplit) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// penaltiesCompatible reports whether incoming leaves the stored split as is.
// A nil incoming split means "not observed" and never differs from old.
func penaltiesCompatible(old, incoming GameXG) bool {
	if incoming.Penalties == nil {
		return true
	}
	return samePenaltySplit(old.Penalties, incoming.Penalties) && old.PenaltyRawJSON == incoming.PenaltyRawJSON
}
