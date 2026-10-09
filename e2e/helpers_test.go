//go:build e2e

package e2e

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	playwright "github.com/mxschmitt/playwright-go"

	"github.com/jrduncans/nwsl-season/internal/asatest"
)

// viewport is a named browser window size.
type viewport struct {
	Name          string
	Width, Height int
}

// Standard viewports. Journeys run at both.
var (
	Desktop = viewport{Name: "desktop", Width: 1280, Height: 800}
	Mobile  = viewport{Name: "mobile", Width: 390, Height: 844}
)

// traceDirEnv names a directory for Playwright traces of failed tests. CI sets
// it and uploads the directory; locally it is usually unset.
const traceDirEnv = "NWSL_E2E_TRACE_DIR"

// logRequestsEnv, when non-empty, logs every page's requests, responses and
// load milestones with timestamps, plus how long visit's checks took. Run with
// -v to see the output.
const logRequestsEnv = "NWSL_E2E_LOG_REQUESTS"

// cspReporter turns CSP violations into console errors, so newPage's console
// listener fails the test on them.
const cspReporter = `document.addEventListener("securitypolicyviolation", (event) => {
	console.error("CSP violation: " + event.violatedDirective + " blocked " + event.blockedURI);
});`

// newPage returns a page in a fresh browser context sized to vp. The test
// fails if the page logs a console error, throws an uncaught error, has a
// same-origin request fail or return a 4xx/5xx status, or violates the CSP.
// The failures are reported when the test ends, after the page has settled, so
// errors raised just after load are caught too.
func newPage(t *testing.T, vp viewport) playwright.Page {
	t.Helper()
	context, err := browser.NewContext(playwright.BrowserNewContextOptions{
		Viewport: &playwright.Size{Width: vp.Width, Height: vp.Height},
	})
	if err != nil {
		t.Fatalf("new browser context: %v", err)
	}
	if err := context.AddInitScript(playwright.Script{Content: playwright.String(cspReporter)}); err != nil {
		t.Fatalf("add CSP init script: %v", err)
	}
	// Tracing snapshots the DOM on every action, which is slow on large
	// pages, so it only runs when failed tests' traces will be kept.
	traceDir := os.Getenv(traceDirEnv)
	if traceDir != "" {
		if err := context.Tracing().Start(playwright.TracingStartOptions{
			Snapshots: playwright.Bool(true),
		}); err != nil {
			t.Fatalf("start tracing: %v", err)
		}
	}
	page, err := context.NewPage()
	if err != nil {
		t.Fatalf("new page: %v", err)
	}

	var (
		mu       sync.Mutex
		problems []string
		closed   bool
	)
	if os.Getenv(logRequestsEnv) != "" {
		began := time.Now()
		logf := func(format string, args ...any) {
			mu.Lock()
			defer mu.Unlock()
			if !closed {
				t.Logf("+%5dms %s", time.Since(began).Milliseconds(), fmt.Sprintf(format, args...))
			}
		}
		page.On("request", func(request playwright.Request) { logf("request  %s %s", request.Method(), request.URL()) })
		page.On("response", func(response playwright.Response) { logf("response %d %s", response.Status(), response.URL()) })
		page.On("domcontentloaded", func(playwright.Page) { logf("domcontentloaded") })
		page.On("load", func(playwright.Page) { logf("load") })
	}
	record := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		problems = append(problems, fmt.Sprintf(format, args...))
	}
	page.On("console", func(message playwright.ConsoleMessage) {
		if message.Type() == "error" {
			record("console error: %s", message.Text())
		}
	})
	page.On("pageerror", func(err error) { record("page error: %v", err) })
	page.On("requestfailed", func(request playwright.Request) {
		if sameOrigin(page, request.URL()) {
			record("request failed: %s %s: %v", request.Method(), request.URL(), request.Failure())
		}
	})
	page.On("response", func(response playwright.Response) {
		if sameOrigin(page, response.URL()) && response.Status() >= 400 {
			record("bad response: %s %s: %d", response.Request().Method(), response.URL(), response.Status())
		}
	})

	t.Cleanup(func() {
		// Let late errors arrive before judging the page: assertions that ran
		// after visit (such as the overflow check) may have triggered some.
		// A failed settle just means the page is already gone.
		_ = settle(page)
		mu.Lock()
		closed = true
		for _, problem := range problems {
			t.Errorf("%s viewport: %s", vp.Name, problem)
		}
		mu.Unlock()

		switch {
		case traceDir == "":
		case t.Failed():
			if err := os.MkdirAll(traceDir, 0o750); err != nil { //nolint:gosec // G703: traceDir is the developer-set trace directory, not request input
				t.Logf("create trace directory: %v", err)
			} else if err := context.Tracing().Stop(filepath.Join(traceDir, traceName(t, vp)+".zip")); err != nil {
				t.Logf("save trace: %v", err)
			}
		default:
			if err := context.Tracing().Stop(); err != nil {
				t.Logf("stop tracing: %v", err)
			}
		}
		if err := context.Close(); err != nil {
			t.Logf("close browser context: %v", err)
		}
	})
	return page
}

var unsafeFileChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func traceName(t *testing.T, vp viewport) string {
	return unsafeFileChars.ReplaceAllString(t.Name()+"-"+vp.Name, "_")
}

// sameOrigin reports whether rawURL has the same scheme and host as the
// page's current document. Before the first navigation the page is
// about:blank, so nothing is same-origin and nothing is flagged.
func sameOrigin(page playwright.Page, rawURL string) bool {
	current, err := url.Parse(page.URL())
	if err != nil || current.Host == "" {
		return false
	}
	other, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return other.Scheme == current.Scheme && other.Host == current.Host
}

// visit loads rawURL, waits for the load event (deferred scripts have run by
// then), and requires a successful status and a visible page heading.
func visit(t *testing.T, page playwright.Page, rawURL string) {
	t.Helper()
	response, err := page.Goto(rawURL, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateLoad,
	})
	if err != nil {
		t.Fatalf("goto %s: %v", rawURL, err)
	}
	if response == nil || !response.Ok() {
		status := 0
		if response != nil {
			status = response.Status()
		}
		t.Fatalf("goto %s: status %d, want 2xx", rawURL, status)
	}
	expect := playwright.NewPlaywrightAssertions()
	if err := expect.Locator(page.Locator("h1").First()).ToBeVisible(); err != nil {
		t.Fatalf("%s has no visible h1: %v", rawURL, err)
	}
	if err := settle(page); err != nil {
		t.Fatalf("%s did not settle: %v", rawURL, err)
	}
	logNavigationTiming(t, page)
}

// settle returns after the page has rendered two animation frames and run its
// already-queued tasks, which is when errors thrown by load-time and
// first-paint script work have been reported. It does not wait on a timer.
// Playwright delivers events in order, so any console, pageerror or request
// event raised before settle returns is recorded before it returns.
func settle(page playwright.Page) error {
	_, err := page.Evaluate(`() => new Promise((resolve) => {
		// The timer only bounds the wait if the browser throttles animation
		// frames for a background page; normally the frames settle first.
		setTimeout(resolve, 2000);
		requestAnimationFrame(() => requestAnimationFrame(() => setTimeout(resolve, 0)));
	})`)
	return err
}

// logNavigationTiming logs the browser's navigation timings and the size of
// the page when logRequestsEnv is set, to show where load time goes.
func logNavigationTiming(t *testing.T, page playwright.Page) {
	t.Helper()
	if os.Getenv(logRequestsEnv) == "" {
		return
	}
	timing, err := page.Evaluate(`() => {
		const nav = performance.getEntriesByType("navigation")[0];
		const resources = performance.getEntriesByType("resource")
			.map((r) => r.name.split("/").pop() + "=" + Math.round(r.responseEnd - r.startTime) + "ms");
		return "response " + Math.round(nav.responseEnd) + "ms, domInteractive " + Math.round(nav.domInteractive) +
			"ms, domContentLoaded " + Math.round(nav.domContentLoadedEventEnd) + "ms, load " + Math.round(nav.loadEventEnd) +
			"ms; " + document.querySelectorAll("*").length + " elements, " +
			document.querySelectorAll("[data-local-time]").length + " local-time; resources: " + resources.join(" ");
	}`)
	if err != nil {
		t.Logf("navigation timing: %v", err)
		return
	}
	t.Logf("%s timing: %v", page.URL(), timing)
}

// assertNoHorizontalOverflow fails the test if the page is wider than its
// viewport, which would make it scroll sideways.
func assertNoHorizontalOverflow(t *testing.T, page playwright.Page) {
	t.Helper()
	result, err := page.Evaluate(`() => ({
		scroll: document.documentElement.scrollWidth,
		client: document.documentElement.clientWidth,
	})`)
	if err != nil {
		t.Fatalf("measure overflow on %s: %v", page.URL(), err)
	}
	measured, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("measure overflow on %s: unexpected result %T", page.URL(), result)
	}
	scroll, client := toFloat(measured["scroll"]), toFloat(measured["client"])
	if scroll > client {
		t.Errorf("%s overflows horizontally: scrollWidth %.0f > clientWidth %.0f", page.URL(), scroll, client)
	}
}

func toFloat(v any) float64 {
	switch n := v.(type) {
	case int:
		return float64(n)
	case float64:
		return n
	default:
		return -1
	}
}

// assertNoASARequests fails the test if the fake ASA received any request
// since its requests were last reset. Page requests must read only the cache.
func assertNoASARequests(t *testing.T, fake *asatest.Server) {
	t.Helper()
	if requests := fake.Requests(); len(requests) > 0 {
		var paths []string
		for _, request := range requests {
			paths = append(paths, request.Path)
		}
		t.Errorf("fake ASA received %d request(s), want none: %s", len(requests), strings.Join(paths, ", "))
	}
}
