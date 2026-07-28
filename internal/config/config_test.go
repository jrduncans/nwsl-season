package config

import (
	"testing"
	"time"
)

func TestFromEnvironmentUsesDefaults(t *testing.T) {
	t.Setenv("NWSL_HTTP_ADDR", "")
	t.Setenv("HOST", "")
	t.Setenv("PORT", "")
	t.Setenv("NWSL_DATA_DIR", "")
	t.Setenv("NWSL_SYNC_SEASON", "")
	t.Setenv("NWSL_SYNC_STAGE", "")
	t.Setenv("NWSL_SYNC_CHECK_INTERVAL", "")
	t.Setenv("NWSL_SYNC_COMPLETION_GRACE", "")
	t.Setenv("NWSL_SYNC_MIN_ATTEMPT_INTERVAL", "")
	t.Setenv("NWSL_SYNC_TIMEOUT", "")
	t.Setenv("NWSL_MAX_SLATE_FIXTURES", "")
	t.Setenv("NWSL_HISTORY_RETENTION", "")
	t.Setenv("NWSL_FORECAST_CONCURRENCY", "")
	t.Setenv("NWSL_FORECAST_TIMEOUT", "")

	got, err := FromEnvironment()
	if err != nil {
		t.Fatal(err)
	}

	if got.HTTPAddr != defaultHTTPAddr {
		t.Errorf("HTTPAddr = %q, want %q", got.HTTPAddr, defaultHTTPAddr)
	}
	if got.DataDir != defaultDataDir {
		t.Errorf("DataDir = %q, want %q", got.DataDir, defaultDataDir)
	}
	if got.DBPath != "data/nwsl-season.sqlite" {
		t.Errorf("DBPath = %q, want %q", got.DBPath, "data/nwsl-season.sqlite")
	}
	if got.SyncSeason != defaultSyncSeason || got.SyncStage != defaultSyncStage {
		t.Errorf("sync season/stage = %q/%q, want %q/%q", got.SyncSeason, got.SyncStage, defaultSyncSeason, defaultSyncStage)
	}
	if got.SyncCheckInterval != defaultSyncCheckInterval || got.SyncCompletionGrace != defaultSyncCompletionGrace || got.SyncMinAttemptInterval != defaultSyncMinAttemptInterval || got.SyncTimeout != defaultSyncTimeout {
		t.Errorf("sync durations = %+v, want defaults", got)
	}
	if got.ForecastConcurrency != defaultForecastConcurrency || got.ForecastTimeout != defaultForecastTimeout {
		t.Errorf("forecast limits = %+v, want defaults", got)
	}
	if got.HistoryRetention != defaultHistoryRetention {
		t.Errorf("history retention = %s, want %s", got.HistoryRetention, defaultHistoryRetention)
	}
	if got.MaxSlateFixtures != defaultMaxSlateFixtures {
		t.Errorf("MaxSlateFixtures = %d, want %d", got.MaxSlateFixtures, defaultMaxSlateFixtures)
	}
}

func TestFromEnvironmentUsesOverrides(t *testing.T) {
	t.Setenv("NWSL_HTTP_ADDR", ":9090")
	t.Setenv("HOST", "0.0.0.0")
	t.Setenv("PORT", "7070")
	t.Setenv("NWSL_DATA_DIR", "testdata")

	t.Setenv("NWSL_SYNC_SEASON", "2027")
	t.Setenv("NWSL_SYNC_STAGE", "Challenge Cup")
	t.Setenv("NWSL_SYNC_CHECK_INTERVAL", "7m")
	t.Setenv("NWSL_SYNC_COMPLETION_GRACE", "4h")
	t.Setenv("NWSL_SYNC_MIN_ATTEMPT_INTERVAL", "45m")
	t.Setenv("NWSL_SYNC_TIMEOUT", "25s")
	t.Setenv("NWSL_MAX_SLATE_FIXTURES", "11")
	t.Setenv("NWSL_HISTORY_RETENTION", "2160h")
	t.Setenv("NWSL_FORECAST_CONCURRENCY", "3")
	t.Setenv("NWSL_FORECAST_TIMEOUT", "40s")

	got, err := FromEnvironment()
	if err != nil {
		t.Fatal(err)
	}

	if got.HTTPAddr != ":9090" {
		t.Errorf("HTTPAddr = %q, want %q", got.HTTPAddr, ":9090")
	}
	if got.DataDir != "testdata" {
		t.Errorf("DataDir = %q, want %q", got.DataDir, "testdata")
	}
	if got.DBPath != "testdata/nwsl-season.sqlite" {
		t.Errorf("DBPath = %q, want %q", got.DBPath, "testdata/nwsl-season.sqlite")
	}
	if got.SyncSeason != "2027" || got.SyncStage != "Challenge Cup" || got.SyncCheckInterval.String() != "7m0s" || got.SyncCompletionGrace.String() != "4h0m0s" || got.SyncMinAttemptInterval.String() != "45m0s" || got.SyncTimeout.String() != "25s" {
		t.Errorf("sync overrides = %+v, want configured values", got)
	}
	if got.ForecastConcurrency != 3 || got.ForecastTimeout != 40*time.Second {
		t.Errorf("forecast overrides = %+v, want configured values", got)
	}
	if got.HistoryRetention != 90*24*time.Hour {
		t.Errorf("history retention = %s, want 90d", got.HistoryRetention)
	}
	if got.MaxSlateFixtures != 11 {
		t.Errorf("MaxSlateFixtures = %d, want 11", got.MaxSlateFixtures)
	}
}

func TestFromEnvironmentBuildsHTTPAddrFromHostAndPort(t *testing.T) {
	t.Setenv("NWSL_HTTP_ADDR", "")
	t.Setenv("HOST", "0.0.0.0")
	t.Setenv("PORT", "9090")

	got, err := FromEnvironment()
	if err != nil {
		t.Fatal(err)
	}

	if got.HTTPAddr != "0.0.0.0:9090" {
		t.Errorf("HTTPAddr = %q, want %q", got.HTTPAddr, "0.0.0.0:9090")
	}
}

func TestFromEnvironmentUsesDefaultHTTPHostWithPortOverride(t *testing.T) {
	t.Setenv("NWSL_HTTP_ADDR", "")
	t.Setenv("HOST", "")
	t.Setenv("PORT", "9090")

	got, err := FromEnvironment()
	if err != nil {
		t.Fatal(err)
	}

	if got.HTTPAddr != "127.0.0.1:9090" {
		t.Errorf("HTTPAddr = %q, want %q", got.HTTPAddr, "127.0.0.1:9090")
	}
}

func TestFromEnvironmentUsesDefaultHTTPPortWithHostOverride(t *testing.T) {
	t.Setenv("NWSL_HTTP_ADDR", "")
	t.Setenv("HOST", "0.0.0.0")
	t.Setenv("PORT", "")

	got, err := FromEnvironment()
	if err != nil {
		t.Fatal(err)
	}

	if got.HTTPAddr != "0.0.0.0:8080" {
		t.Errorf("HTTPAddr = %q, want %q", got.HTTPAddr, "0.0.0.0:8080")
	}
}

func TestFromEnvironmentRejectsInvalidSyncDuration(t *testing.T) {
	for _, value := range []string{"invalid", "0s", "-1m"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("NWSL_SYNC_TIMEOUT", value)
			if _, err := FromEnvironment(); err == nil {
				t.Fatal("FromEnvironment error = nil, want validation error")
			}
		})
	}
}

func TestFromEnvironmentRejectsInvalidForecastLimits(t *testing.T) {
	for _, test := range []struct {
		name, key, value string
	}{
		{"concurrency", "NWSL_FORECAST_CONCURRENCY", "0"},
		{"concurrency non-number", "NWSL_FORECAST_CONCURRENCY", "many"},
		{"timeout", "NWSL_FORECAST_TIMEOUT", "0s"},
		{"history retention", "NWSL_HISTORY_RETENTION", "0s"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(test.key, test.value)
			if _, err := FromEnvironment(); err == nil {
				t.Fatalf("FromEnvironment() with %s=%q succeeded", test.key, test.value)
			}
		})
	}
}
