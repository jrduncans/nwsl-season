package competition

import "testing"

func TestForSeasonReturnsValidatedDefensiveRules(t *testing.T) {
	rules, ok := ForSeason("2026", "Regular Season")
	if !ok || rules.Validate() != nil || len(rules.Achievements) != 3 {
		t.Fatalf("rules = %+v, ok=%v", rules, ok)
	}
	if rules.Version != "2026-regular-v2" || rules.Achievements[1].Label != "Top-four seed" {
		t.Fatalf("2026 qualification semantics = %+v, want v2 top-four seed", rules)
	}
	rules.Achievements[0].TopK = 99
	again, _ := ForSeason("2026", "Regular Season")
	if again.Achievements[0].TopK != 1 {
		t.Fatal("catalog slice was mutable")
	}
	if _, ok := ForSeason("2027", "Regular Season"); ok {
		t.Fatal("unknown season received defaults")
	}
}
func TestRulesRejectNonMonotonicAchievements(t *testing.T) {
	r := Rules{Season: "x", Stage: "s", Version: "v", ExpectedTeams: 2, GamesPerTeam: 1, Achievements: []Achievement{{ID: "a", Label: "a", TopK: 2}, {ID: "b", Label: "b", TopK: 1}}}
	if r.Validate() == nil {
		t.Fatal("expected invalid ordering")
	}
}
