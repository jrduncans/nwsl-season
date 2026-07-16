package scenarios

import (
	"context"
	"sort"
	"strings"

	"github.com/jrduncans/nwsl-season/internal/clinching"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

func minimize(ctx context.Context, r Request, games []standings.Game, initial []Clause, d *Diagnostics) ([]Clause, error) {
	memo := map[string]bool{}
	final := map[string]Clause{}
	var deleteOne func([]FixtureCondition) error
	deleteOne = func(c []FixtureCondition) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		removed := false
		for i := range c {
			next := append([]FixtureCondition(nil), c[:i]...)
			next = append(next, c[i+1:]...)
			ok, methods, err := certify(ctx, r, next, d, memo)
			if err != nil {
				return err
			}
			if ok {
				removed = true
				if err := deleteOne(next); err != nil {
					return err
				}
				_ = methods
			}
		}
		if !removed {
			key := clauseKey(c)
			if _, ok := final[key]; !ok {
				final[key] = Clause{Conditions: append([]FixtureCondition(nil), c...), RepresentedAssignments: represented(c, len(games)), ProofMethods: []clinching.ProofMethod{}}
			}
		}
		return nil
	}
	for _, c := range initial {
		if err := deleteOne(c.Conditions); err != nil {
			return nil, err
		}
	}
	values := make([]Clause, 0, len(final))
	for _, c := range final {
		ok, m, err := certify(ctx, r, c.Conditions, d, memo)
		if err != nil {
			return nil, err
		}
		if ok {
			c.ProofMethods = m
			values = append(values, c)
		}
	}
	changed := true
	for changed {
		changed = false
	outer:
		for i := 0; i < len(values); i++ {
			for j := i + 1; j < len(values); j++ {
				merged, ok := mergeCandidate(values[i].Conditions, values[j].Conditions)
				if !ok {
					continue
				}
				d.CombinationProbes++
				valid, m, err := certify(ctx, r, merged, d, memo)
				if err != nil {
					return nil, err
				}
				if valid {
					values[i] = Clause{Conditions: merged, RepresentedAssignments: represented(merged, len(games)), ProofMethods: m}
					values = append(values[:j], values[j+1:]...)
					changed = true
					break outer
				}
			}
		}
	}
	keep := make([]Clause, 0, len(values))
	for i, c := range values {
		dominated := false
		for j, other := range values {
			if i != j && subsumes(other.Conditions, c.Conditions) && !subsumes(c.Conditions, other.Conditions) {
				dominated = true
				break
			}
		}
		if !dominated {
			sortConditions(c.Conditions, r.Slate.FixtureIDs)
			c.ProofMethods = sortedMethods(c.ProofMethods)
			keep = append(keep, c)
		}
	}
	sort.Slice(keep, func(i, j int) bool {
		if len(keep[i].Conditions) != len(keep[j].Conditions) {
			return len(keep[i].Conditions) < len(keep[j].Conditions)
		}
		if keep[i].RepresentedAssignments != keep[j].RepresentedAssignments {
			return keep[i].RepresentedAssignments > keep[j].RepresentedAssignments
		}
		return clauseKey(keep[i].Conditions) < clauseKey(keep[j].Conditions)
	})
	return keep, nil
}
func certify(ctx context.Context, r Request, c []FixtureCondition, d *Diagnostics, memo map[string]bool) (bool, []clinching.ProofMethod, error) {
	key := clauseKey(c)
	if v, ok := memo[key]; ok {
		return v, []clinching.ProofMethod{}, nil
	}
	d.MinimizationProbes++
	methods := []clinching.ProofMethod{}
	var probe func(int, []clinching.FixedResult) (bool, error)
	probe = func(i int, f []clinching.FixedResult) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if i == len(c) {
			v, err := r.Evaluator.EvaluateStatus(ctx, r.TargetTeamID, r.Achievement, f)
			d.OracleCalls++
			if err != nil {
				return false, err
			}
			if !publishable(v) {
				return false, nil
			}
			methods = append(methods, v.Method)
			return true, nil
		}
		for _, o := range c[i].AllowedOutcomes {
			ok, err := probe(i+1, append(append([]clinching.FixedResult(nil), f...), clinching.FixedResult{GameID: c[i].GameID, Outcome: o}))
			if err != nil || !ok {
				return false, err
			}
		}
		return true, nil
	}
	ok, err := probe(0, nil)
	memo[key] = ok
	return ok, sortedMethods(methods), err
}
func clauseKey(c []FixtureCondition) string {
	p := make([]string, len(c))
	for i, f := range c {
		os := canonicalOutcomes(f.AllowedOutcomes)
		s := make([]string, len(os))
		for j, o := range os {
			s[j] = string(o)
		}
		p[i] = f.GameID + "=" + strings.Join(s, "|")
	}
	sort.Strings(p)
	return strings.Join(p, ";")
}
func mergeCandidate(a, b []FixtureCondition) ([]FixtureCondition, bool) {
	if len(a) != len(b) {
		return nil, false
	}
	am := map[string][]clinching.Outcome{}
	bm := map[string][]clinching.Outcome{}
	for _, f := range a {
		am[f.GameID] = f.AllowedOutcomes
	}
	for _, f := range b {
		bm[f.GameID] = f.AllowedOutcomes
	}
	diff := ""
	for id, x := range am {
		y, ok := bm[id]
		if !ok {
			return nil, false
		}
		if strings.Join(outcomeStrings(x), ",") != strings.Join(outcomeStrings(y), ",") {
			if diff != "" {
				return nil, false
			}
			diff = id
		}
	}
	if diff == "" {
		return nil, false
	}
	out := []FixtureCondition{}
	for _, f := range a {
		os := f.AllowedOutcomes
		if f.GameID == diff {
			os = canonicalOutcomes(append(os, bm[diff]...))
			if len(os) == 3 {
				continue
			}
		}
		out = append(out, FixtureCondition{GameID: f.GameID, AllowedOutcomes: canonicalOutcomes(os)})
	}
	return out, true
}
func outcomeStrings(v []clinching.Outcome) []string {
	x := canonicalOutcomes(v)
	out := make([]string, len(x))
	for i, o := range x {
		out[i] = string(o)
	}
	return out
}
func subsumes(a, b []FixtureCondition) bool {
	bm := map[string]map[clinching.Outcome]bool{}
	for _, f := range b {
		m := map[clinching.Outcome]bool{}
		for _, o := range f.AllowedOutcomes {
			m[o] = true
		}
		bm[f.GameID] = m
	}
	for _, f := range a {
		m, ok := bm[f.GameID]
		if !ok {
			return false
		}
		for _, o := range f.AllowedOutcomes {
			if !m[o] {
				return false
			}
		}
	}
	return true
}
