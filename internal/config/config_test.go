package config

import "testing"

func TestFromEnvironmentUsesDefaults(t *testing.T) {
	t.Setenv("NWSL_HTTP_ADDR", "")
	t.Setenv("HOST", "")
	t.Setenv("PORT", "")
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
	t.Setenv("HOST", "0.0.0.0")
	t.Setenv("PORT", "7070")
	t.Setenv("NWSL_DATA_DIR", "testdata")

	got := FromEnvironment()

	if got.HTTPAddr != ":9090" {
		t.Errorf("HTTPAddr = %q, want %q", got.HTTPAddr, ":9090")
	}
	if got.DataDir != "testdata" {
		t.Errorf("DataDir = %q, want %q", got.DataDir, "testdata")
	}
}

func TestFromEnvironmentBuildsHTTPAddrFromHostAndPort(t *testing.T) {
	t.Setenv("NWSL_HTTP_ADDR", "")
	t.Setenv("HOST", "0.0.0.0")
	t.Setenv("PORT", "9090")

	got := FromEnvironment()

	if got.HTTPAddr != "0.0.0.0:9090" {
		t.Errorf("HTTPAddr = %q, want %q", got.HTTPAddr, "0.0.0.0:9090")
	}
}

func TestFromEnvironmentUsesDefaultHTTPHostWithPortOverride(t *testing.T) {
	t.Setenv("NWSL_HTTP_ADDR", "")
	t.Setenv("HOST", "")
	t.Setenv("PORT", "9090")

	got := FromEnvironment()

	if got.HTTPAddr != "127.0.0.1:9090" {
		t.Errorf("HTTPAddr = %q, want %q", got.HTTPAddr, "127.0.0.1:9090")
	}
}

func TestFromEnvironmentUsesDefaultHTTPPortWithHostOverride(t *testing.T) {
	t.Setenv("NWSL_HTTP_ADDR", "")
	t.Setenv("HOST", "0.0.0.0")
	t.Setenv("PORT", "")

	got := FromEnvironment()

	if got.HTTPAddr != "0.0.0.0:8080" {
		t.Errorf("HTTPAddr = %q, want %q", got.HTTPAddr, "0.0.0.0:8080")
	}
}
