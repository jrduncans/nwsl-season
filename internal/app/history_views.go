package app

import (
	"fmt"
	"strings"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/history"
)

type historyPage struct {
	Title, HomePath, StylesheetPath, ScriptPath, FormPath string
	CatalogPage                                           bool
	Navigation                                            []navigationItem
	Years                                                 []historyYearView
	Rows                                                  []historyRowView
	Selected                                              *historyRowView
	EligibleYears                                         string
	ExcludedSeasons                                       []historyExclusionView
}

type historyYearView struct {
	Season, Path string
	Selected     bool
}

type historyRowView struct {
	Season, Path, Status, Inventory, Exclusions, GoalsPerMatch string
	Played                                                     int
	TotalGoals                                                 int64
	Selected                                                   bool
}

type historyExclusionView struct {
	Season, Reason string
}

func historyPageFor(fromPath string, summaries []history.SeasonScoring, selectedSeason string) historyPage {
	page := historyPage{
		Title:          "Scoring by season",
		HomePath:       relativeURL(fromPath, "/"),
		StylesheetPath: relativeURL(fromPath, "/static/site.css"),
		ScriptPath:     relativeURL(fromPath, "/static/standings.js"),
		FormPath:       historyURL(fromPath, ""),
		CatalogPage:    true,
		Navigation: []navigationItem{
			{Label: "Seasons", Path: relativeURL(fromPath, "/seasons")},
			{Label: "History", Path: historyURL(fromPath, ""), Current: true},
		},
		Years:           make([]historyYearView, 0, len(summaries)),
		Rows:            make([]historyRowView, 0, len(summaries)),
		ExcludedSeasons: make([]historyExclusionView, 0, len(summaries)),
	}
	eligible := make([]string, 0, len(summaries))
	for _, summary := range summaries {
		row := historyRow(summary, fromPath, selectedSeason)
		page.Rows = append(page.Rows, row)
		page.Years = append(page.Years, historyYearView{Season: summary.Season, Path: row.Path, Selected: row.Selected})
		if summary.PlotEligible {
			eligible = append(eligible, summary.Season)
		}
		if row.Exclusions != "" {
			page.ExcludedSeasons = append(page.ExcludedSeasons, historyExclusionView{Season: row.Season, Reason: row.Exclusions})
		}
		if row.Selected {
			selected := row
			page.Selected = &selected
		}
	}
	if len(eligible) == 0 {
		page.EligibleYears = "No seasons currently meet the comparison requirements."
	} else {
		page.EligibleYears = "Currently eligible for comparison: " + strings.Join(eligible, ", ") + "."
	}
	return page
}

func historyRow(summary history.SeasonScoring, fromPath, selectedSeason string) historyRowView {
	goalsPerMatch := "Unavailable"
	if summary.GoalsPerMatch != nil {
		goalsPerMatch = fmt.Sprintf("%.2f", *summary.GoalsPerMatch)
	}
	return historyRowView{
		Season: summary.Season, Path: historyURL(fromPath, summary.Season), Selected: summary.Season == selectedSeason,
		Played: summary.Played, TotalGoals: summary.TotalGoals, GoalsPerMatch: goalsPerMatch,
		Status: historyStatus(summary), Inventory: historyInventory(summary), Exclusions: exclusionText(summary.Exclusions),
	}
}

func historyStatus(summary history.SeasonScoring) string {
	lifecycle := "Season status unavailable"
	switch summary.Lifecycle {
	case cache.SourceScopeUpcoming:
		lifecycle = "Upcoming"
	case cache.SourceScopeActive:
		lifecycle = fmt.Sprintf("Active through %d matches", summary.Played)
	case cache.SourceScopeCompleted:
		lifecycle = "Completed"
	}
	if summary.Readiness == cache.SourceReadinessAvailable {
		return lifecycle
	}
	availability := "Source data unavailable"
	if summary.Readiness == cache.SourceReadinessNotPublished {
		availability = "Not published"
	}
	return lifecycle + "; " + availability
}

func historyInventory(summary history.SeasonScoring) string {
	switch summary.Inventory {
	case cache.InventoryCompletenessComplete:
		return "Fixture inventory complete"
	case cache.InventoryCompletenessIncomplete:
		return "Known fixture inventory incomplete"
	default:
		if summary.Readiness == cache.SourceReadinessAvailable {
			return "Cached matches; inventory unverified"
		}
		return "Inventory unavailable"
	}
}
