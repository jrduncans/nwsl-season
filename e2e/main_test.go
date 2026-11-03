//go:build e2e

// Package e2e drives the real server in sandboxed headless stable Chrome.
// Run it with `make test-e2e`; a plain `go test ./...` never builds it.
// Install Chrome separately and the driver with `make e2e-install`. Tests
// never download anything.
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
	"os/exec"
	"strings"
	"testing"

	playwright "github.com/mxschmitt/playwright-go"
)

const installHint = "Install stable Google Chrome and run `make e2e-install` for the Playwright driver"

// browser is shared by every test in the process. Each test gets its own
// browser context from newPage, so tests do not share cookies or storage.
var browser playwright.Browser

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	// Use a maintained Node installation, never the driver's bundled runtime.
	nodePath := os.Getenv("PLAYWRIGHT_NODEJS_PATH")
	if nodePath == "" {
		var err error
		nodePath, err = exec.LookPath("node")
		if err != nil {
			fmt.Fprintln(os.Stderr, "Node.js is required; run `mise install`")
			return 1
		}
		if err := os.Setenv("PLAYWRIGHT_NODEJS_PATH", nodePath); err != nil {
			fmt.Fprintf(os.Stderr, "set Node.js runtime: %v\n", err)
			return 1
		}
	}
	nodeVersion, err := exec.Command(nodePath, "--version").Output() //nolint:gosec // G204: developer-selected Node executable with a fixed --version argument
	if err != nil {
		fmt.Fprintf(os.Stderr, "check Node.js runtime: %v\n", err)
		return 1
	}
	proxy, closeProxy := newBrowserProxy()
	defer closeProxy()

	// playwright.Run only starts the already-installed driver; it never
	// downloads one. `make e2e-install` installs only the matching driver.
	pw, err := playwright.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s (playwright driver: %v)\n", installHint, err)
		return 1
	}
	defer func() { _ = pw.Stop() }()

	options := playwright.BrowserTypeLaunchOptions{
		Headless:        playwright.Bool(true),
		Channel:         playwright.String("chrome"),
		ChromiumSandbox: playwright.Bool(true),
		Proxy: &playwright.Proxy{
			Server: proxy.URL,
			// Chromium normally bypasses proxies for loopback destinations.
			Bypass: playwright.String("<-loopback>"),
		},
	}
	override := os.Getenv("NWSL_E2E_CHROMIUM")
	if override != "" {
		options.Channel = nil
		options.ExecutablePath = playwright.String(override)
	}
	browser, err = pw.Chromium.Launch(options)
	if err != nil {
		if override == "" && strings.Contains(err.Error(), "Executable doesn't exist") {
			fmt.Fprintf(os.Stderr, "%s\n", installHint)
		} else {
			fmt.Fprintf(os.Stderr, "launch Chrome with Chromium sandbox required (no unsandboxed fallback): %v\n%s\n", err, installHint)
		}
		return 1
	}
	defer func() { _ = browser.Close() }()

	driver, err := playwright.NewDriver()
	if err != nil {
		fmt.Fprintf(os.Stderr, "inspect Playwright driver: %v\n", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "E2E browser %s; Chromium sandbox required; Playwright %s; Node %s\n",
		browser.Version(), driver.Version, strings.TrimSpace(string(nodeVersion)))
	return m.Run()
}
