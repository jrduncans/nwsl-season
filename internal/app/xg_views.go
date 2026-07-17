package app

import (
	"fmt"
	"net/url"
	"sort"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
)

type xgPage struct {
	Title, Season, HomePath, StylesheetPath, ScriptPath, SeasonPath, ScheduleDifficultyPath string
	Navigation                                                                              []navigationItem
	Rows                                                                                    []xgRowView
	Available, Completed                                                                    int
	Freshness, FreshnessFallback, XGFreshness, XGFreshnessFallback                          string
}
type xgRowView struct {
	Team                                             teamNameView
	Matches                                          int
	For, Against, Difference, DifferencePer, Missing string
	sortDifference                                   float64
	hasXG                                            bool
}

func xgPageFrom(data cache.SeasonData, season, from string, location *time.Location) xgPage {
	p := xgPage{Title: "Expected goals · " + season + " NWSL season", Season: season, HomePath: relativeURL(from, "/"), StylesheetPath: relativeURL(from, "/static/site.css"), ScriptPath: relativeURL(from, "/static/standings.js"), SeasonPath: seasonURL(from, season), ScheduleDifficultyPath: relativeURL(from, "/seasons/"+url.PathEscape(season)+"/schedule-difficulty"), Navigation: seasonNavigation(from, season, "/seasons/"+url.PathEscape(season)+"/xg")}
	type total struct {
		matches, missing int
		forXG, against   float64
	}
	totals := map[string]*total{}
	for _, team := range data.Teams {
		totals[team.ID] = &total{}
	}
	for _, game := range data.Games {
		if game.Status == fixtures.CompletedStatus {
			p.Completed++
			if totals[game.HomeTeamID] != nil {
				totals[game.HomeTeamID].missing++
			}
			if totals[game.AwayTeamID] != nil {
				totals[game.AwayTeamID].missing++
			}
		}
	}
	for _, xg := range data.XGoals {
		if xg.Availability != cache.XGAvailable || !xg.HomeXG.Valid || !xg.AwayXG.Valid {
			continue
		}
		h, a := totals[xg.HomeTeamID], totals[xg.AwayTeamID]
		if h == nil || a == nil {
			continue
		}
		p.Available++
		h.matches++
		h.missing--
		h.forXG += xg.HomeXG.Float64
		h.against += xg.AwayXG.Float64
		a.matches++
		a.missing--
		a.forXG += xg.AwayXG.Float64
		a.against += xg.HomeXG.Float64
	}
	for _, team := range data.Teams {
		v := totals[team.ID]
		row := xgRowView{Team: teamName(team), Matches: v.matches, Missing: fmt.Sprint(v.missing)}
		if v.matches == 0 {
			row.For, row.Against, row.Difference, row.DifferencePer = "Unavailable", "Unavailable", "Unavailable", "Unavailable"
		} else {
			row.hasXG = true
			row.sortDifference = (v.forXG - v.against) / float64(v.matches)
			row.For = fmt.Sprintf("%.2f", v.forXG)
			row.Against = fmt.Sprintf("%.2f", v.against)
			row.Difference = fmt.Sprintf("%.2f", v.forXG-v.against)
			row.DifferencePer = fmt.Sprintf("%.2f", row.sortDifference)
		}
		p.Rows = append(p.Rows, row)
	}
	sort.Slice(p.Rows, func(i, j int) bool {
		left, right := p.Rows[i], p.Rows[j]
		if left.hasXG != right.hasXG {
			return left.hasXG
		}
		if left.sortDifference != right.sortDifference {
			return left.sortDifference > right.sortDifference
		}
		return left.Team.Name < right.Team.Name
	})
	if data.LastSuccess != nil {
		p.Freshness, p.FreshnessFallback = freshnessValues(data.LastSuccess.FinishedAt, location)
	}
	if data.XGStatus.LastSuccess != nil {
		p.XGFreshness, p.XGFreshnessFallback = freshnessValues(data.XGStatus.LastSuccess.FinishedAt, location)
	}
	return p
}
