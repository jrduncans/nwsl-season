package history

import (
	"slices"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/apptest"
	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/competition"
)

func thresholdSeason(t *testing.T, rows []SeasonThresholds, year string) SeasonThresholds {
	t.Helper()
	for _, row := range rows {
		if row.Season == year {
			return row
		}
	}
	t.Fatalf("missing threshold season %s", year)
	return SeasonThresholds{}
}

func TestThresholdsDeriveRecordedSeasons(t *testing.T) {
	archive := apptest.Scenario(t, apptest.ScenarioThresholds)
	summaries, err := SummarizeScoring(archive)
	if err != nil {
		t.Fatal(err)
	}
	rows := Thresholds(summaries, archive)
	if len(rows) != 9 {
		t.Fatal(len(rows))
	}
	for _, row := range rows {
		for _, value := range row.Values {
			if !value.Applicable {
				continue
			}
			if value.PPG == nil {
				t.Fatalf("%s %s: %s", row.Season, value.Label, value.Reason)
			}
			if *value.PPG != float64(value.Cutoff.OfficialPoints)/float64(value.Cutoff.OfficialPlayed) {
				t.Fatal("wrong PPG basis")
			}
		}
	}
	if value := thresholdSeason(t, rows, "2022").Values[1]; !value.Tied || value.Cutoff.ID != "4JMAk47qKg" || value.Next.ID != "4wM4rZdqjB" {
		t.Fatalf("home tie inferred incorrectly: %+v", value)
	}
	if value := thresholdSeason(t, rows, "2023").Values[3]; !value.Tied || value.Cutoff.ID != "raMyrr25d2" || value.Next.ID != "XVqKeVKM01" {
		t.Fatalf("playoff tie inferred incorrectly: %+v", value)
	}
	if value := thresholdSeason(t, rows, "2021").Values[0]; value.Cutoff.Earned != 41 || value.Cutoff.Played != 23 || value.Cutoff.OfficialPoints != 44 || value.Cutoff.OfficialPlayed != 24 || *value.PPG != 44.0/24 {
		t.Fatalf("forfeit PPG: %+v", value)
	}
	if value := thresholdSeason(t, rows, "2019").Values[3]; value.Next.ID != "aDQ0lzvQEv" {
		t.Fatal("2019 head-to-head order not derived")
	}
}

func TestThresholdsWithholdIncompleteAndAmbiguousEvidence(t *testing.T) {
	archive := apptest.Scenario(t, apptest.ScenarioThresholds)
	for i := range archive {
		if archive[i].Entry.Season == "2024" {
			archive[i].Data.Games = archive[i].Data.Games[1:]
		}
		if archive[i].Entry.Season == "2023" {
			archive[i].PlayoffGames = archive[i].PlayoffGames[:2]
		}
	}
	summaries, err := SummarizeScoring(archive)
	if err != nil {
		t.Fatal(err)
	}
	for _, year := range []string{"2023", "2024"} {
		for _, value := range thresholdSeason(t, Thresholds(summaries, archive), year).Values {
			if value.PPG != nil || value.Applicable && value.Reason == "" {
				t.Fatal("incomplete evidence published")
			}
		}
	}
	// The 2019 non-playoff tie resolves from results without an exception.
	for _, snapshot := range archive {
		if snapshot.Entry.Season != "2019" {
			continue
		}
		for _, summary := range summaries {
			if summary.Season != "2019" {
				continue
			}
			playoff, _ := competition.Lookup("2019", "Playoffs")
			row := deriveThresholdSeason(snapshot.Entry, summary, snapshot, *playoff.BracketFormat, competition.OutcomeOverrides{})
			if row.Values[3].PPG == nil || row.Values[0].PPG == nil || row.Values[1].PPG == nil {
				t.Fatal("2019 tie was not computed from cached matches")
			}
		}
	}
}

func TestThresholdAdjustmentsDoNotDuplicateAwards(t *testing.T) {
	overrides := competition.HistoricalOutcomeOverrides()["2021"]
	makeFinishes := func() map[string]ThresholdFinish {
		return map[string]ThresholdFinish{
			"Pk5LeeNqOW": {Played: 23, Earned: 41, OfficialPlayed: 23, OfficialPoints: 41},
			"7vQ7BBzqD1": {Played: 23, Earned: 39, OfficialPlayed: 23, OfficialPoints: 39},
			"aDQ0lzvQEv": {Played: 22, Earned: 39, OfficialPlayed: 22, OfficialPoints: 39},
			"other":      {Played: 24, OfficialPlayed: 24},
		}
	}
	finishes := makeFinishes()
	if reason := applyThresholdAdjustments(finishes, overrides); reason != "" {
		t.Fatal(reason)
	}
	if reason := applyThresholdAdjustments(finishes, overrides); reason != "" || finishes["Pk5LeeNqOW"].OfficialPoints != 44 {
		t.Fatal("award applied twice", reason, finishes)
	}
	// ASA supplies one award itself: apply only the other missing fixture.
	finishes = makeFinishes()
	for _, id := range []string{"7vQ7BBzqD1", "aDQ0lzvQEv"} {
		finish := finishes[id]
		finish.Played++
		finish.OfficialPlayed++
		if id == "7vQ7BBzqD1" {
			finish.Earned += 3
			finish.OfficialPoints += 3
		}
		finishes[id] = finish
	}
	if reason := applyThresholdAdjustments(finishes, overrides); reason != "" || finishes["7vQ7BBzqD1"].OfficialPoints != 42 || finishes["aDQ0lzvQEv"].OfficialPlayed != 24 {
		t.Fatal("partially supplied awards", reason, finishes)
	}
	deductions := map[string]ThresholdFinish{"kRQa8JOqKZ": {Played: 26, Earned: 27, OfficialPlayed: 26, OfficialPoints: 27}}
	if reason := applyThresholdAdjustments(deductions, competition.HistoricalOutcomeOverrides()["2024"]); reason != "" || deductions["kRQa8JOqKZ"].OfficialPoints != 24 || deductions["kRQa8JOqKZ"].Earned != 27 {
		t.Fatal("deduction", reason, deductions)
	}
}

func TestThresholdsNewCompletedSeasonNeedsNoOutcomeRecord(t *testing.T) {
	archive := apptest.Scenario(t, apptest.ScenarioThresholds)
	index := slices.IndexFunc(archive, func(s cache.HistoricalSeason) bool { return s.Entry.Season == "2024" })
	snapshot := archive[index]
	snapshot.Entry.Season = "2026"
	snapshot.Readiness.Scope.Season = "2026"
	snapshot.Readiness.Scope.Lifecycle = cache.SourceScopeActive
	snapshot.Readiness.Completeness = cache.InventoryCompletenessComplete
	for i := range snapshot.Data.Games {
		snapshot.Data.Games[i].Season = "2026"
	}
	for i := range snapshot.PlayoffGames {
		snapshot.PlayoffGames[i].Season = "2026"
	}
	summaries, err := SummarizeScoring([]cache.HistoricalSeason{snapshot})
	if err != nil {
		t.Fatal(err)
	}
	row := thresholdSeason(t, Thresholds(summaries, []cache.HistoricalSeason{snapshot}), "2026")
	for _, value := range row.Values {
		if value.Applicable && value.PPG == nil {
			t.Fatalf("new season needs manual record: %s", value.Reason)
		}
	}
	summaries[0].Pending = 1
	for _, row := range Thresholds(summaries, []cache.HistoricalSeason{snapshot}) {
		if row.Season == "2026" {
			for _, value := range row.Values {
				if value.PPG != nil {
					t.Fatal("active unfinished season published")
				}
			}
		}
	}
}

func TestThresholdRankingIncludesAwardedScoresAndRejectsComputedTieOverrides(t *testing.T) {
	archive := apptest.Scenario(t, apptest.ScenarioThresholds)
	summaries, err := SummarizeScoring(archive)
	if err != nil {
		t.Fatal(err)
	}
	for _, snapshot := range archive {
		if snapshot.Entry.Season != "2021" && snapshot.Entry.Season != "2019" {
			continue
		}
		for _, summary := range summaries {
			if summary.Season != snapshot.Entry.Season {
				continue
			}
			playoff, _ := competition.Lookup(summary.Season, "Playoffs")
			overrides := competition.HistoricalOutcomeOverrides()[summary.Season]
			season := deriveThresholdSeason(snapshot.Entry, summary, snapshot, *playoff.BracketFormat, overrides)
			finishes, _, reason := thresholdFinishes(summary, snapshot, *playoff.BracketFormat, season, overrides)
			if reason != "" {
				t.Fatal(reason)
			}
			if summary.Season == "2021" {
				ranked := thresholdRanking(snapshot, finishes, overrides)
				// Independent cached goals sum; the forfeit adds three goals and a win.
				for _, award := range overrides.Awards {
					goals, wins := 0, 0
					for _, g := range snapshot.Data.Games {
						if !g.HomeScore.Valid || !g.AwayScore.Valid {
							continue
						}
						if g.HomeTeamID == award.Winner {
							goals += int(g.HomeScore.Int64)
							if g.HomeScore.Int64 > g.AwayScore.Int64 {
								wins++
							}
						}
						if g.AwayTeamID == award.Winner {
							goals += int(g.AwayScore.Int64)
							if g.AwayScore.Int64 > g.HomeScore.Int64 {
								wins++
							}
						}
					}
					for _, row := range ranked {
						if row.Team.ID == award.Winner && (row.Record.GoalsFor != goals+3 || row.Record.Wins != wins+1 || row.Record.Played != 24) {
							t.Fatalf("award missing from tiebreak record: %+v", row)
						}
					}
				}
			} else {
				overrides.Ties = []competition.OutcomeTie{{Order: []string{"eV5D2w9QKn", "aDQ0lzvQEv"}, Source: "https://example.com"}}
				bad := deriveThresholdSeason(snapshot.Entry, summary, snapshot, *playoff.BracketFormat, overrides)
				if bad.Values[3].PPG != nil || bad.Values[3].Reason == "" {
					t.Fatal("override replaced computable historical order")
				}
			}
		}
	}
}
