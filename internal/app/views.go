package app

import (
	"fmt"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/standings"
	"github.com/jrduncans/nwsl-season/internal/whatif"
)

type seasonPage struct {
	Title         string
	Season        string
	Stage         string
	SeasonPath    string
	WhatIfPath    string
	Source        string
	Freshness     string
	ClinchingNote string
	ScheduleNote  string
	Standings     []tableRowView
	Projected     []tableRowView
	FixtureGroups []fixtureGroupView
	Selections    int
	Remaining     int
}

type tableRowView struct {
	Position       int
	TeamID         string
	Team           string
	Played         int
	Wins           int
	Draws          int
	Losses         int
	GoalsFor       int
	GoalsAgainst   int
	GoalDifference string
	Points         int
	PointsPerGame  string
	PlayoffLine    bool
	Clinched       bool
	TieBreak       string
}

type fixtureGroupView struct {
	Label string
	Games []fixtureView
}

type fixtureView struct {
	ID           string
	Kickoff      string
	HomeTeam     string
	AwayTeam     string
	Score        string
	Completed    bool
	Remaining    bool
	Status       string
	SelectedHome bool
	SelectedDraw bool
	SelectedAway bool
}

type errorPage struct {
	Title   string
	Message string
}

func tableViews(table []standings.TableRow, playoffPlaces int, clinched map[string]bool) []tableRowView {
	rows := make([]tableRowView, 0, len(table))
	for index, row := range table {
		gd := row.Record.GoalDifference()
		gdText := intText(gd)
		if gd > 0 {
			gdText = "+" + gdText
		}
		tieBreak := ""
		if row.TieBreak.Undetermined {
			tieBreak = "Official order unresolved: " + row.TieBreak.Reason
		}
		rows = append(rows, tableRowView{
			Position: index + 1, TeamID: row.Team.ID, Team: displayName(row.Team),
			Played: row.Record.Played, Wins: row.Record.Wins, Draws: row.Record.Draws, Losses: row.Record.Losses,
			GoalsFor: row.Record.GoalsFor, GoalsAgainst: row.Record.GoalsAgainst, GoalDifference: gdText,
			Points: row.Record.Points, PointsPerGame: fmt.Sprintf("%.2f", pointsPerGame(row.Record)),
			PlayoffLine: index+1 == playoffPlaces, Clinched: clinched != nil && clinched[row.Team.ID], TieBreak: tieBreak,
		})
	}
	return rows
}

func fixtureGroups(data cache.SeasonData, selections map[string]whatif.Outcome, location *time.Location) []fixtureGroupView {
	teams := make(map[string]string, len(data.Teams))
	for _, team := range data.Teams {
		teams[team.ID] = displayName(team)
	}
	groups := []fixtureGroupView{}
	groupIndex := map[string]int{}
	for _, game := range data.Games {
		kickoff, _ := parseKickoff(game.KickoffUTC)
		localKickoff := kickoff.In(location)
		label := localKickoff.Format("Monday, January 2")
		if game.Matchday.Valid {
			label = fmt.Sprintf("Matchday %d", game.Matchday.Int64)
		}
		index, ok := groupIndex[label]
		if !ok {
			index = len(groups)
			groupIndex[label] = index
			groups = append(groups, fixtureGroupView{Label: label})
		}
		view := fixtureView{
			ID: game.ASAID, Kickoff: localKickoff.Format("Mon Jan 2, 3:04 PM MST"),
			HomeTeam: teams[game.HomeTeamID], AwayTeam: teams[game.AwayTeamID],
			Completed: game.Status == standings.CompletedStatus,
			Remaining: game.Status == whatif.RemainingStatus,
			Status:    game.Status,
		}
		if view.Completed && game.HomeScore.Valid && game.AwayScore.Valid {
			view.Score = fmt.Sprintf("%d–%d", game.HomeScore.Int64, game.AwayScore.Int64)
		}
		switch selections[game.ASAID] {
		case whatif.HomeWin:
			view.SelectedHome = true
		case whatif.Draw:
			view.SelectedDraw = true
		case whatif.AwayWin:
			view.SelectedAway = true
		}
		groups[index].Games = append(groups[index].Games, view)
	}
	return groups
}

func displayName(team standings.Team) string {
	for _, value := range []string{team.Name, team.ShortName, team.Abbreviation, team.ID} {
		if value != "" {
			return value
		}
	}
	return "Unknown team"
}

func pointsPerGame(record standings.Record) float64 {
	if record.Played == 0 {
		return 0
	}
	return float64(record.Points) / float64(record.Played)
}
