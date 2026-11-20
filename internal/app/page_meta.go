package app

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Each page builds its own <title> and meta description from data its
// handler already has, so search results and link previews tell pages apart.

const (
	siteName = "NWSL by the Numbers"
	// siteDescription is the meta description for pages without their own,
	// such as error pages.
	siteDescription = "Standings, results, xG, clinching scenarios, and playoff forecasts for the NWSL."
	titleSuffix     = " · " + siteName
	// maxTitleLength is about where search results truncate a title. The site
	// name is dropped rather than letting it push a page's own title past it.
	maxTitleLength = 60
)

// documentTitle is the <title> for a page titled title: the title with the
// site name appended when both fit within maxTitleLength.
func documentTitle(title string) string {
	if utf8.RuneCountInString(title+titleSuffix) <= maxTitleLength {
		return title + titleSuffix
	}
	return title
}

// pageDescription is the meta description: the page's own, or the site's.
func pageDescription(description string) string {
	if description == "" {
		return siteDescription
	}
	return description
}

// stageTitle names the stage at the start of a title: "2026 NWSL" for the
// regular season, "2026 NWSL playoff" for the playoffs, and the catalog label
// for other stages.
func stageTitle(scope requestCompetition) string {
	switch scope.Stage {
	case "Regular Season":
		return scope.Season + " NWSL"
	case "Playoffs":
		return scope.Season + " NWSL playoff"
	}
	return scope.Entry.Label
}

// stageAdjective names the stage before a noun in a description: "2026 NWSL
// regular-season table", "2026 NWSL playoff bracket".
func stageAdjective(scope requestCompetition) string {
	switch scope.Stage {
	case "Regular Season":
		return scope.Season + " NWSL regular-season"
	case "Playoffs":
		return scope.Season + " NWSL playoff"
	}
	return scope.Entry.Label
}

// stageNoun names the stage on its own: "the 2026 NWSL regular season".
func stageNoun(scope requestCompetition) string {
	switch scope.Stage {
	case "Regular Season":
		return "the " + scope.Season + " NWSL regular season"
	case "Playoffs":
		return "the " + scope.Season + " NWSL playoffs"
	}
	return "the " + scope.Entry.Label
}

// standingsMeta describes a season page: the table once there is one, or
// the season overview before it starts.
func standingsMeta(scope requestCompetition, page seasonPage) (string, string) {
	if !page.HasStandings {
		return stageTitle(scope) + " season", "Standings, results and forecasts for " + stageNoun(scope) + "."
	}
	measures := "points and goal difference"
	if page.HasXG {
		measures = "points, goal difference and expected goals"
	}
	return stageTitle(scope) + " standings", fmt.Sprintf("%s table with %s for all %d teams.", stageAdjective(scope), measures, len(page.Standings))
}

// fixturesMeta describes a results and fixtures page by what it lists.
func fixturesMeta(scope requestCompetition, page seasonPage) (string, string) {
	details := "with scores"
	if page.HasXG {
		details = "with scores and xG"
	}
	switch {
	case page.HasResults && page.HasUpcomingFixtures:
		return stageTitle(scope) + " results and fixtures", fmt.Sprintf("Every %s result and upcoming fixture, %s.", stageAdjective(scope), details)
	case page.HasResults:
		return stageTitle(scope) + " results", fmt.Sprintf("Every %s result, %s.", stageAdjective(scope), details)
	}
	return stageTitle(scope) + " fixtures", fmt.Sprintf("Every %s fixture, with kickoff times.", stageAdjective(scope))
}

func bracketMeta(scope requestCompetition) (string, string) {
	return stageTitle(scope) + " bracket", fmt.Sprintf("Every round of the %s bracket, with matchups and results.", stageAdjective(scope))
}

func scheduleDifficultyMeta(scope requestCompetition) (string, string) {
	return stageTitle(scope) + " schedule difficulty", fmt.Sprintf("How hard each team's remaining %s schedule is, by opponent points per game, venue and schedule load.", stageAdjective(scope))
}

func forecastMeta(scope requestCompetition) (string, string) {
	return scope.Season + " NWSL playoff forecast", fmt.Sprintf("Simulated odds for each team to make the %s NWSL playoffs, win the Shield and more. Fix results to test your own scenarios.", scope.Season)
}

func playoffsForecastMeta(scope requestCompetition) (string, string) {
	return scope.Season + " NWSL championship forecast", fmt.Sprintf("Simulated odds for each %s NWSL playoff team to reach each round and win the championship, and what each remaining match would change.", scope.Season)
}

func clinchingMeta(scope requestCompetition) (string, string) {
	return stageTitle(scope) + " clinching scenarios", fmt.Sprintf("What each team needs to clinch a %s NWSL playoff spot, a better seed or the Shield, and which results would eliminate it.", scope.Season)
}

// modelEvaluationMeta describes the model evaluation, which is the same
// checked-in evidence on every season's page, so it names no season.
func modelEvaluationMeta() (string, string) {
	return "NWSL forecast model evaluation", "How closely each forecast model predicted the final NWSL regular-season table at different points in past seasons."
}

// matchMeta names the meeting: the score once it's played, the date before,
// so two teams' several meetings don't share a title.
func matchMeta(scope requestCompetition, match fixtureView, location *time.Location, outlook bool) (string, string) {
	home, away := match.HomeTeam.Name, match.AwayTeam.Name
	competition := scope.Season + " NWSL"
	if scope.Stage == "Playoffs" {
		competition += " playoffs"
	}
	var day, date string
	if kickoff, err := time.Parse(time.RFC3339, match.KickoffUTC); err == nil {
		local := kickoff.In(location)
		day, date = local.Format("Jan 2"), local.Format("Jan 2, 2006")
	}
	on := ""
	if day != "" {
		on = " on " + day
	}
	if match.Completed && match.Score != "" {
		homeGoals, awayGoals, _ := strings.Cut(match.Score, "–")
		title := fmt.Sprintf("%s %s %s, %s", home, match.Score, away, competition)
		return title, matchResult(home, away, homeGoals, awayGoals, match) + " in " + stageNoun(scope) + on + matchXG(match) + "."
	}
	title := home + " vs " + away
	if date != "" {
		title += ", " + date
	}
	summary := "how the teams compare"
	if outlook {
		summary += " and the match outlook"
	}
	return title, fmt.Sprintf("%s vs %s in %s%s: %s.", home, away, stageNoun(scope), on, summary)
}

// matchResult says who won, winner first, so the score reads naturally.
func matchResult(home, away, homeGoals, awayGoals string, match fixtureView) string {
	score := homeGoals + "–" + awayGoals
	if homeGoals == awayGoals {
		switch match.Winner {
		case "home":
			return fmt.Sprintf("%s beat %s on penalties after a %s draw", home, away, score)
		case "away":
			return fmt.Sprintf("%s beat %s on penalties after a %s draw", away, home, score)
		}
		return fmt.Sprintf("%s and %s drew %s", home, away, score)
	}
	if match.Winner == "away" {
		return fmt.Sprintf("%s won %s–%s at %s", away, awayGoals, homeGoals, home)
	}
	return fmt.Sprintf("%s beat %s %s", home, away, score)
}

// matchXG adds the xG, oriented like the score in the sentence.
func matchXG(match fixtureView) string {
	if len(match.XGLines) == 0 {
		return ""
	}
	xg := match.XGLines[0]
	if match.Winner == "away" {
		return fmt.Sprintf(" (xG %s–%s)", xg.Away, xg.Home)
	}
	return fmt.Sprintf(" (xG %s–%s)", xg.Home, xg.Away)
}
