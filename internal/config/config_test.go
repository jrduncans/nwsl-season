package config

import "testing"

func TestFromEnvironmentUsesDefaults(t *testing.T) {
	t.Setenv("NWSL_HTTP_ADDR", "")
	t.Setenv("NWSL_DATA_DIR", "")

	got := FromEnvironment()

	if got.HTTPAddr != defaultHTTPAddr {
		t.Errorf("HTTPAddr = %q, want %q", got.HTTPAddr, defaultHTTPAddr)
	}
	if got.DataDir != defaultDataDir {
		t.Errorf("DataDir = %q, want %q", got.DataDir, defaultDataDir)
	}
}

func TestFromEnvironmentUsesOverrides(t *testing.T) {
	t.Setenv("NWSL_HTTP_ADDR", ":9090")
	t.Setenv("NWSL_DATA_DIR", "testdata")

	got := FromEnvironment()

	if got.HTTPAddr != ":9090" {
		t.Errorf("HTTPAddr = %q, want %q", got.HTTPAddr, ":9090")
	}
	if got.DataDir != "testdata" {
		t.Errorf("DataDir = %q, want %q", got.DataDir, "testdata")
	}
}
