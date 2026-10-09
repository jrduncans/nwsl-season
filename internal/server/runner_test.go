package server

import (
	"testing"

	"github.com/jrduncans/nwsl-season/internal/cache"
)

func TestForecastInputsChanged(t *testing.T) {
	tests := []struct {
		name string
		run  cache.SyncRun
		want bool
	}{
		{name: "unchanged", run: cache.SyncRun{}, want: false},
		{name: "team inserted", run: cache.SyncRun{TeamsInserted: 1}, want: true},
		{name: "team updated", run: cache.SyncRun{TeamsUpdated: 1}, want: true},
		{name: "game inserted", run: cache.SyncRun{GamesInserted: 1}, want: true},
		{name: "game updated", run: cache.SyncRun{GamesUpdated: 1}, want: true},
		{name: "game deleted", run: cache.SyncRun{GamesDeleted: 1}, want: true},
		{name: "xg inserted", run: cache.SyncRun{XGRun: &cache.XGSyncRun{RowsInserted: 1}}, want: true},
		{name: "xg updated", run: cache.SyncRun{XGRun: &cache.XGSyncRun{RowsUpdated: 1}}, want: true},
		{name: "xg unchanged", run: cache.SyncRun{XGRun: &cache.XGSyncRun{RowsUnchanged: 1}}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := forecastInputsChanged(test.run); got != test.want {
				t.Errorf("forecastInputsChanged(%+v) = %t, want %t", test.run, got, test.want)
			}
		})
	}
}

func TestForecastWarmingRunnerUsesOnlyCurrentAndPreviousTwoRegularSeasons(t *testing.T) {
	runner := forecastWarmingRunner{currentSeason: "2026", currentStage: "Regular Season"}
	for _, test := range []struct {
		season, stage string
		want          bool
	}{
		{"2026", "Regular Season", true},
		{"2025", "Regular Season", true},
		{"2024", "Regular Season", true},
		{"2023", "Regular Season", false},
		{"2025", "Playoffs", false},
	} {
		if got := runner.forecastInputsForScope(test.season, test.stage); got != test.want {
			t.Errorf("forecastInputsForScope(%q, %q) = %t, want %t", test.season, test.stage, got, test.want)
		}
	}
}
