package competition

import (
	"net/url"
	"testing"
)

func TestOutcomeOverridesAreSparseAndSourceBacked(t *testing.T) {
	overrides := HistoricalOutcomeOverrides()
	if len(overrides) != 2 {
		t.Fatalf("exception seasons: %d", len(overrides))
	}
	for year, override := range overrides {
		var sources []string
		for _, award := range override.Awards {
			if award.Winner == "" || award.Loser == "" || award.Winner == award.Loser || (award.Home != award.Winner && award.Home != award.Loser) || award.WinnerGoals <= award.LoserGoals || award.LoserGoals < 0 {
				t.Fatalf("%s invalid award: %+v", year, award)
			}
			sources = append(sources, award.Source)
		}
		for _, deduction := range override.Deductions {
			if deduction.Team == "" || deduction.Points <= 0 {
				t.Fatalf("%s invalid deduction: %+v", year, deduction)
			}
			sources = append(sources, deduction.Source)
		}
		for _, tie := range override.Ties {
			seen := map[string]bool{}
			if len(tie.Order) < 2 {
				t.Fatalf("%s invalid tie", year)
			}
			for _, id := range tie.Order {
				if id == "" || seen[id] {
					t.Fatalf("%s invalid tie identity", year)
				}
				seen[id] = true
			}
			sources = append(sources, tie.Source)
		}
		for _, source := range sources {
			parsed, err := url.Parse(source)
			if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
				t.Fatalf("%s invalid source: %s", year, source)
			}
		}
	}
	overrides["2021"].Awards[0].Winner = "changed"
	if HistoricalOutcomeOverrides()["2021"].Awards[0].Winner == "changed" {
		t.Fatal("caller changed embedded overrides")
	}
}
