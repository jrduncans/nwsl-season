package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnvironmentFileValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.env")
	contents := "# managed by 1Password\nHONEYCOMB_API_KEY=key#with-symbols\nexport OTEL_SERVICE_NAME=\"nwsl season\"\nSINGLE_QUOTED='a value'\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	values, err := environmentFileValues(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []environmentValue{
		{name: "HONEYCOMB_API_KEY", value: "key#with-symbols"},
		{name: "OTEL_SERVICE_NAME", value: "nwsl season"},
		{name: "SINGLE_QUOTED", value: "a value"},
	}
	if len(values) != len(want) {
		t.Fatalf("values = %#v, want %#v", values, want)
	}
	for index := range want {
		if values[index] != want[index] {
			t.Errorf("values[%d] = %#v, want %#v", index, values[index], want[index])
		}
	}
}

func TestEnvironmentFileValuesRejectsInvalidLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.env")
	if err := os.WriteFile(path, []byte("NOT_AN_ASSIGNMENT\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := environmentFileValues(path); err == nil {
		t.Fatal("environmentFileValues() error = nil, want error")
	}
}

func TestLoadEnvironmentFileUsesFileWithoutOverridingProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.env")
	const fileOnly = "NWSL_TEST_FILE_ONLY"
	const processWins = "NWSL_TEST_PROCESS_WINS"
	if err := os.WriteFile(path, []byte(fileOnly+"=from-file\n"+processWins+"=from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NWSL_CONFIG_FILE", path)
	t.Setenv(processWins, "from-process")
	oldValue, wasSet := os.LookupEnv(fileOnly)
	if err := os.Unsetenv(fileOnly); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if wasSet {
			_ = os.Setenv(fileOnly, oldValue)
		} else {
			_ = os.Unsetenv(fileOnly)
		}
	})

	if err := LoadEnvironmentFile(); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv(fileOnly); got != "from-file" {
		t.Errorf("%s = %q, want from-file", fileOnly, got)
	}
	if got := os.Getenv(processWins); got != "from-process" {
		t.Errorf("%s = %q, want from-process", processWins, got)
	}
}

func TestLoadEnvironmentFileIgnoresMissingFile(t *testing.T) {
	t.Setenv("NWSL_CONFIG_FILE", "")
	if err := LoadEnvironmentFile(); err != nil {
		t.Fatal(err)
	}
}

func TestLoadEnvironmentFileIgnoresMissingConfiguredFile(t *testing.T) {
	t.Setenv("NWSL_CONFIG_FILE", filepath.Join(t.TempDir(), "missing.env"))
	if err := LoadEnvironmentFile(); err != nil {
		t.Fatal(err)
	}
}
