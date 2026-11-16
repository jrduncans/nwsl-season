package app

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
)

func stadiumGame(id, home, away, stadium string) cache.Game {
	game := cache.Game{ASAID: id, HomeTeamID: home, AwayTeamID: away}
	if stadium != "" {
		game.RawJSON = fmt.Sprintf(`{"stadium_id":%q}`, stadium)
	}
	return game
}

func TestChampionshipHostMatchesAFinalistsUsualStadium(t *testing.T) {
	regular := []cache.Game{
		stadiumGame("1", "was", "kc", "audi"), stadiumGame("2", "was", "por", "audi"),
		stadiumGame("3", "was", "sea", "segra"), // One alternate venue does not change the usual one.
		stadiumGame("4", "kc", "was", "cpkc"), stadiumGame("5", "kc", "por", "cpkc"),
		stadiumGame("6", "por", "kc", "providence"),
		stadiumGame("7", "tie", "kc", "one"), stadiumGame("8", "tie", "was", "two"),
	}
	for _, c := range []struct {
		name  string
		final cache.Game
		want  string
	}{
		{"home finalist at home", stadiumGame("f", "was", "kc", "audi"), "was"},
		{"away finalist at home", stadiumGame("f", "kc", "was", "audi"), "was"},
		{"neither finalist's stadium", stadiumGame("f", "was", "kc", "providence"), ""},
		{"alternate venue is not usual", stadiumGame("f", "was", "kc", "segra"), ""},
		{"tied usual stadium is unknown", stadiumGame("f", "tie", "kc", "one"), ""},
		{"no stadium", stadiumGame("f", "was", "kc", ""), ""},
		{"unidentified finalist", stadiumGame("f", "was", "", "audi"), ""},
	} {
		if got := championshipHost(regular, c.final); got != c.want {
			t.Errorf("%s: host = %q, want %q", c.name, got, c.want)
		}
	}
}

// hostedFinalStore plays the 2026 final at the away finalist's usual stadium.
func hostedFinalStore(t *testing.T) (handoffStore, cache.Game) {
	t.Helper()
	regular := resolvedHandoffSeason()
	for i := range regular.Games {
		regular.Games[i].RawJSON = fmt.Sprintf(`{"stadium_id":"home-of-%s"}`, regular.Games[i].HomeTeamID)
	}
	seeds := playoffSeedsFrom("2026", regular)
	games := []cache.Game{
		forecastPlayoffGame(seeds, "qf1", 1, 8, true), forecastPlayoffGame(seeds, "qf2", 4, 5, true),
		forecastPlayoffGame(seeds, "qf3", 2, 7, true), forecastPlayoffGame(seeds, "qf4", 3, 6, true),
		forecastPlayoffGame(seeds, "sf1", 1, 4, true), forecastPlayoffGame(seeds, "sf2", 2, 3, true),
		forecastPlayoffGame(seeds, "final", 2, 1, false),
	}
	for i := range games {
		games[i].KickoffUTC = time.Date(2026, 11, 1+i, 20, 0, 0, 0, time.UTC).Format(time.RFC3339)
	}
	final := &games[len(games)-1]
	final.RawJSON = fmt.Sprintf(`{"stadium_id":"home-of-%s"}`, final.AwayTeamID)
	return handoffStore{regular: regular, playoffs: cache.SeasonData{Teams: regular.Teams, Games: games}}, *final
}

func TestHostedFinalGivesTheHostHomeAdvantageEverywhere(t *testing.T) {
	store, final := hostedFinalStore(t)
	a := &application{store: store}
	state, err := a.forecastPostseason(context.Background(), "2026", store.regular, 8)
	if err != nil || state == nil || state.Unavailable != "" {
		t.Fatalf("postseason = %+v, %v", state, err)
	}
	if got := state.Slots["final"]; !got.Hosted || got.Home != final.AwayTeamID || got.Away != final.HomeTeamID {
		t.Fatalf("final slot = %+v, want hosted by %s", got, final.AwayTeamID)
	}

	odds := a.playoffOutlooks(context.Background(), "2026", store.playoffs)["final"]
	body := handoffResponse(t, store, "/seasons/2026/playoffs/matches/final")
	var awayName string
	for _, team := range store.regular.Teams {
		if team.ID == final.AwayTeamID {
			awayName = team.Name
		}
	}
	requireText(t, body, ".match-meta", "Final", "At "+awayName+"'s stadium")
	forbidText(t, body, ".match-meta", "Neutral venue")
	requireAttr(t, body, ".match-venue input[checked]", "value", "away")
	requireText(t, body, ".match-venue", "Neutral", "home (actual)")
	// The page and the bracket agree on the hosted final.
	requireText(t, body, ".match-board", "Advance "+odds.HomeAdvanceText, "Advance "+odds.AwayAdvanceText, "Match outlook")
	requireText(t, body, ".match-factors", "League home rate", "League away rate")
	forbidText(t, body, ".match-factors", "League neutral rate")

	neutral := handoffResponse(t, store, "/seasons/2026/playoffs/matches/final?venue=neutral")
	requireAttr(t, neutral, ".match-venue input[checked]", "value", "neutral")
	requireText(t, neutral, ".fixture-outlook-model", "If neutral")
	requireText(t, neutral, ".match-factors", "League neutral rate")
	// The actual venue stays stated while the outlook assumes another.
	requireText(t, neutral, ".match-meta", "At "+awayName+"'s stadium")

	home := handoffResponse(t, store, "/seasons/2026/playoffs/matches/final?venue=home")
	requireText(t, home, ".fixture-outlook-model", "If ", " home")
	// Moving home advantage to the page's home team changes its advance chance.
	if got := text(find(t, home, ".match-board-team")[0]); strings.Contains(got, "Advance "+odds.HomeAdvanceText) {
		t.Errorf("home team card %q kept the away-hosted advance chance", got)
	}

	if bogus := handoffResponse(t, store, "/seasons/2026/playoffs/matches/final?venue=elsewhere"); scopeText(t, bogus, ".fixture-outlook-model") != "Match outlook" {
		t.Errorf("an unknown venue changed the outlook label to %q", scopeText(t, bogus, ".fixture-outlook-model"))
	}
}
