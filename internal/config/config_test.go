package config

import "testing"

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
