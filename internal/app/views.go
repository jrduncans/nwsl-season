package app

import (
	"fmt"
	"net/url"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/standings"
	"github.com/jrduncans/nwsl-season/internal/strength"
	"github.com/jrduncans/nwsl-season/internal/whatif"
)

const clubLogoBaseURL = "https://american-soccer-analysis-headshots.s3.amazonaws.com/club_logos/"

type seasonPage struct {
	Title          string
	Season         string
	Stage          string
	HomePath       string
	StylesheetPath string
	ScriptPath     string
	SeasonPath     string
	WhatIfPath     string
	Source         string
	Freshness      string
	ClinchingNote  string
	ScheduleNote   string
	Standings      []tableRowView
	Strength       strengthView
	Projected      []tableRowView
	FixtureGroups  []fixtureGroupView
	Selections     int
	Remaining      int
}

type strengthView struct {
	Rows             []strengthRowView
	CompletedMatches int
	RemainingMatches int
	HomePPG          string
	AwayPPG          string
	VenueGap         string
}

type strengthRowView struct {
	Position                 int
	Team                     teamNameView
	RemainingFixtures        int
	RemainingHome            int
	RemainingAway            int
	HomeOpponentPPG          string
	AwayOpponentPPG          string
	RawOpponentPPG           string
	VenueAdjustedOpponentPPG string
	Available                bool
}

type tableRowView struct {
	Position       int
	TeamID         string
	Team           teamNameView
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
	HomeTeam     teamNameView
	AwayTeam     teamNameView
	Score        string
	Completed    bool
	Remaining    bool
	Status       string
	SelectedHome bool
	SelectedDraw bool
	SelectedAway bool
}

type teamNameView struct {
	Name    string
	LogoURL string
}

type errorPage struct {
	Title          string
	Message        string
	HomePath       string
	StylesheetPath string
	ScriptPath     string
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
			Position: index + 1, TeamID: row.Team.ID, Team: teamName(row.Team),
			Played: row.Record.Played, Wins: row.Record.Wins, Draws: row.Record.Draws, Losses: row.Record.Losses,
			GoalsFor: row.Record.GoalsFor, GoalsAgainst: row.Record.GoalsAgainst, GoalDifference: gdText,
			Points: row.Record.Points, PointsPerGame: fmt.Sprintf("%.2f", pointsPerGame(row.Record)),
			PlayoffLine: index+1 == playoffPlaces, Clinched: clinched != nil && clinched[row.Team.ID], TieBreak: tieBreak,
		})
	}
	return rows
}

func strengthViewFrom(result strength.Result) strengthView {
	rows := make([]strengthRowView, 0, len(result.Rows))
	for index, row := range result.Rows {
		view := strengthRowView{
			Position: index + 1, Team: teamName(row.Team), RemainingFixtures: row.RemainingFixtures,
			RemainingHome: row.RemainingHome, RemainingAway: row.RemainingAway, Available: row.Available,
		}
		if row.Available {
			view.HomeOpponentPPG = fmt.Sprintf("%.2f", row.HomeOpponentPPG)
			view.AwayOpponentPPG = fmt.Sprintf("%.2f", row.AwayOpponentPPG)
			view.RawOpponentPPG = fmt.Sprintf("%.2f", row.RawOpponentPPG)
			view.VenueAdjustedOpponentPPG = fmt.Sprintf("%.2f", row.VenueAdjustedOpponentPPG)
		} else {
			view.HomeOpponentPPG, view.AwayOpponentPPG = "—", "—"
			view.RawOpponentPPG, view.VenueAdjustedOpponentPPG = "—", "—"
		}
		rows = append(rows, view)
	}
	return strengthView{
		Rows: rows, CompletedMatches: result.CompletedMatches, RemainingMatches: result.RemainingMatches,
		HomePPG: fmt.Sprintf("%.2f", result.HomePPG), AwayPPG: fmt.Sprintf("%.2f", result.AwayPPG), VenueGap: fmt.Sprintf("%.2f", result.VenueGap),
	}
}

func fixtureGroups(data cache.SeasonData, selections map[string]whatif.Outcome, location *time.Location) []fixtureGroupView {
	teams := make(map[string]teamNameView, len(data.Teams))
	for _, team := range data.Teams {
		teams[team.ID] = teamName(team)
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

func teamName(team standings.Team) teamNameView {
	return teamNameView{Name: displayName(team), LogoURL: clubLogoURL(team.ID)}
}

func clubLogoURL(teamID string) string {
	if teamID == "" {
		return ""
	}
	return clubLogoBaseURL + url.PathEscape(teamID) + ".png"
}

func pointsPerGame(record standings.Record) float64 {
	if record.Played == 0 {
		return 0
	}
	return float64(record.Points) / float64(record.Played)
}
