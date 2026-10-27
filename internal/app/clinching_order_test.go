package app

import (
	"reflect"
	"slices"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/clinching"
)

func TestClinchingFixturePresentationOrder(t *testing.T) {
	teams := map[string]string{"uta": "UTA", "njy": "NJY", "sea": "SEA"}
	games := map[string]cache.Game{
		"z-first":  {HomeTeamID: "njy", AwayTeamID: "uta", KickoffUTC: "2026-10-25T19:00:00Z"},
		"a-second": {HomeTeamID: "uta", AwayTeamID: "sea", KickoffUTC: "2026-11-01 19:00:00 UTC"},
	}
	ids := []string{"a-second", "z-first"}
	if got := noHelpFixtureText(clinching.NoHelpPath{FixtureIDs: ids}, "uta", games, teams); got != "at NJY and vs SEA" {
		t.Fatal(got)
	}
	if got := partsText(rivalFixtureParts(ids, "uta", teams, games)); got != "(at NJY, vs SEA)" {
		t.Fatal(got)
	}
	r := scenarioRequirement{GameID: ids[0], SecondGameID: ids[1], TeamID: "uta", Points: 4}
	if got := partsText(requirementParts(r, "uta", teams, games)); got != requirementText(r, "uta", teams, games) {
		t.Fatal(got)
	}
	if !slices.Equal(ids, []string{"a-second", "z-first"}) {
		t.Fatal("mutated stored IDs")
	}
	// Different outcomes must follow the fixture they describe, including when
	// an away win precedes a home draw; the score stays on this alternative.
	rs := []scenarioRequirement{{GameID: ids[0], Mask: maskDraw}, {GameID: ids[1], Mask: maskAwayWin}, summaryScore("uta", "sea", 2, false)}
	original := slices.Clone(rs)
	e := scenarioExpression{Alternatives: []scenarioExpression{{Conditions: rs}, {Conditions: []scenarioRequirement{{GameID: ids[0], Mask: maskHomeWin}}}}}
	presentExpression(&e, teams, games, nil, scenarioHistory{})
	got := e.Alternatives[0].Conditions
	if got[0].GameID != "z-first" || got[0].Mask != maskAwayWin || got[1].GameID != "a-second" || got[1].Mask != maskDraw || got[2].Score != rs[2].Score {
		t.Fatalf("fixture/outcome association changed: %+v", got)
	}
	if !reflect.DeepEqual(rs, original) {
		t.Fatal("mutated stored requirements")
	}
	if len(e.Alternatives[1].Conditions) != 1 || e.Alternatives[1].Conditions[0].Mask != maskHomeWin {
		t.Fatal("moved score into another alternative")
	}
	heading := partsText(ownHeadingParts(clinchingGroupView{Own: rs[:2]}, "uta", teams, games))
	if heading != "Win at NJY and draw vs SEA" {
		t.Fatal(heading)
	}
}

func TestClinchingFixtureOrderFallback(t *testing.T) {
	for _, kickoff := range []string{"", "invalid", "2026-10-25T19:00:00Z"} {
		games := map[string]cache.Game{"b": {KickoffUTC: kickoff}, "a": {KickoffUTC: kickoff}}
		if got := fixtureIDsInKickoffOrder([]string{"b", "a"}, games); !slices.Equal(got, []string{"a", "b"}) {
			t.Fatal(got)
		}
	}
	games := map[string]cache.Game{"a": {KickoffUTC: "2026-10-25T19:00:00Z"}, "b": {KickoffUTC: "2026-10-25 19:00:00 UTC"}}
	if got := fixtureIDsInKickoffOrder([]string{"b", "a"}, games); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatal(got)
	}
}
