//go:build e2e

// Package e2e drives the real server in a headless Chromium. Run it with
// `make test-e2e`; a plain `go test ./...` never builds it. Install the
// browser first with `make e2e-install`. Tests never download anything.
//
// Every top-level test calls t.Parallel (the paralleltest linter enforces
// it): Go starts parallel tests only after every sequential one has finished,
// so a single sequential test delays the whole suite. Build server
// configuration with testConfig rather than t.Setenv, which parallel tests
// cannot use, and pass e2eForecastIterations to server.Build.
package e2e

import (
	"fmt"
	"os"
	"strings"
	"testing"

	playwright "github.com/mxschmitt/playwright-go"
)

const installHint = "Chromium is not installed; run `make e2e-install`"

// browser is shared by every test in the process. Each test gets its own
// browser context from newPage, so tests do not share cookies or storage.
var browser playwright.Browser

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	// playwright.Run only starts the already-installed driver; it never
	// downloads one. `make e2e-install` installs the driver and Chromium.
	pw, err := playwright.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s (playwright driver: %v)\n", installHint, err)
		return 1
	}
	defer func() { _ = pw.Stop() }()

	options := playwright.BrowserTypeLaunchOptions{Headless: playwright.Bool(true)}
	override := os.Getenv("NWSL_E2E_CHROMIUM")
	if override != "" {
		options.ExecutablePath = playwright.String(override)
	}
	browser, err = pw.Chromium.Launch(options)
	if err != nil {
		if override == "" && strings.Contains(err.Error(), "Executable doesn't exist") {
			fmt.Fprintf(os.Stderr, "%s\n", installHint)
		} else {
			fmt.Fprintf(os.Stderr, "launch Chromium: %v\n", err)
		}
		return 1
	}
	defer func() { _ = browser.Close() }()

	return m.Run()
}
