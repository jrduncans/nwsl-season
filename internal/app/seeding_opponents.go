package app

import (
	"fmt"
	"sort"

	"github.com/jrduncans/nwsl-season/internal/standings"
)

type seedingOpponentChanceView struct {
	Team                 *teamNameView
	Label                string
	Likelihood, BarWidth string
	Other                bool
}

// Sum unrounded joint shares across every reachable seed and own-result
// column. Keep the full forecast denominator, including missing playoffs and
// outcomes omitted by disciplinary ties or the exact scenario search. With no
// possible opponent there is no outlook to show.
func seedingOpponentChances(rows []seedingRowView, teams []standings.Team, available bool) []seedingOpponentChanceView {
	shares := map[string]float64{}
	missPossible, miss, counted := false, 0.0, 0.0
	for _, row := range rows {
		counted += row.probability
		if row.seed == 0 {
			missPossible = true
			miss += row.probability
			continue
		}
		for _, id := range row.opponentIDs {
			shares[id] += row.opponentLikelihoods[id]
		}
	}
	if len(shares) == 0 {
		return nil
	}
	identities := map[string]teamNameView{}
	for _, team := range teams {
		identities[team.ID] = teamName(team)
	}
	ids := make([]string, 0, len(shares))
	for id := range shares {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if shares[ids[i]] != shares[ids[j]] {
			return shares[ids[i]] > shares[ids[j]]
		}
		if identities[ids[i]].Name != identities[ids[j]].Name {
			return identities[ids[i]].Name < identities[ids[j]].Name
		}
		return ids[i] < ids[j]
	})
	chance := func(label string, p float64, only bool) seedingOpponentChanceView {
		view := seedingOpponentChanceView{Label: label, Likelihood: "Odds unavailable"}
		if available {
			view.Likelihood = likelihoodText(p, only && p >= 1-1e-12)
			if p > 0 {
				view.BarWidth = fmt.Sprintf("%.6f%%", min(1, max(0, p))*100)
			}
		}
		return view
	}
	out := make([]seedingOpponentChanceView, 0, len(ids)+2)
	for _, id := range ids {
		team, ok := identities[id]
		if !ok {
			team = teamNameView{ID: id, Name: id, LogoURL: clubLogoURL(id)}
		}
		view := chance(team.Name, shares[id], len(ids) == 1 && !missPossible)
		view.Team = &team
		out = append(out, view)
	}
	if missPossible {
		view := chance("Miss playoffs", miss, len(ids) == 0)
		view.Other = true
		out = append(out, view)
	}
	if available && counted < 1-1e-9 {
		view := chance("Unresolved outcome", 1-counted, counted <= 1e-12)
		view.Other = true
		out = append(out, view)
	}
	return out
}
