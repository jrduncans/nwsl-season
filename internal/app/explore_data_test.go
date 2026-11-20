package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/apptest"
	"github.com/jrduncans/nwsl-season/internal/cache"
)

// explorePayloadFields maps each browser payload, by the id of the script
// element that once embedded it, to the linked part and member that now
// carry it.
var explorePayloadFields = map[string]struct{ link, member string }{
	"explore-data":              {"data-basis-url", "chartData"},
	"explore-team-data":         {"data-basis-url", "teamSeasons"},
	"explore-context-data":      {"data-basis-url", "historyContext"},
	"explore-home-data":         {"data-basis-url", "homeAdvantage"},
	"explore-h2h-data":          {"data-shared-url", "h2h"},
	"explore-team-seasons-data": {"data-shared-url", "teamSeasons"},
	"explore-threshold-data":    {"data-shared-url", "thresholds"},
	"explore-parity-data":       {"data-shared-url", "parity"},
	"explore-views":             {"data-shared-url", "views"},
}

// explorePayload fetches one payload that the Explore page at pagePath links
// from body, through handler, and returns its JSON.
func explorePayload(t testing.TB, handler http.Handler, pagePath, body, id string) string {
	t.Helper()
	field, ok := explorePayloadFields[id]
	if !ok {
		t.Fatalf("unknown Explore payload %q", id)
	}
	response := getExploreData(t, handler, pagePath, body, field.link)
	if response.Code != http.StatusOK {
		t.Fatalf("%s: status %d", id, response.Code)
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &members); err != nil {
		t.Fatalf("%s: %v", id, err)
	}
	return string(members[field.member])
}

// archivePayload is explorePayload for a page that getPage rendered.
func archivePayload(t testing.TB, archive []cache.HistoricalSeason, pagePath, body, id string) string {
	t.Helper()
	return explorePayload(t, NewHandlerWithOptions(&archiveHTTPStore{archive: archive}, Options{ForecastIterations: 20}), pagePath, body, id)
}

// exploreDataLink resolves the page's payload link attr against pagePath.
func exploreDataLink(t testing.TB, pagePath, body, attr string) string {
	t.Helper()
	link, ok := lookupAttr(find(t, body, "[data-explore]")[0], attr)
	if !ok {
		t.Fatalf("Explore page has no %s", attr)
	}
	base, err := url.Parse(pagePath)
	if err != nil {
		t.Fatal(err)
	}
	target, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	return base.ResolveReference(target).String()
}

func getExploreData(t testing.TB, handler http.Handler, pagePath, body, attr string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, exploreDataLink(t, pagePath, body, attr), nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

// TestExploreDataMatchesPage checks that every selection on a basis links
// the same payload versions, and that the endpoint serves exactly those
// versions, which it may then mark immutable.
func TestExploreDataMatchesPage(t *testing.T) {
	handler := NewHandlerWithOptions(&archiveHTTPStore{archive: apptest.Scenario(t, apptest.ScenarioTeamHistory)}, Options{ForecastIterations: 20})
	queries := []string{"", "view=teams&display=table&season=2025", "view=head-to-head&team=kRQa8JOqKZ", "view=team-history&team=kRQa8JOqKZ", "view=season-trend&team=kRQa8JOqKZ", "view=team-seasons", "view=parity", "view=home-advantage"}
	for _, item := range apptest.ExplorePointsOnlyCases() {
		queries = append(queries, item.Query)
	}
	links := map[string]map[string]string{}
	for _, query := range queries {
		for _, penalties := range []string{"", "penalties=exclude"} {
			target := exploreTarget(strings.Trim(query+"&"+penalties, "&"))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
			if response.Code == http.StatusFound || response.Code == http.StatusPermanentRedirect {
				continue // A legacy or default selection redirects.
			}
			if response.Code != http.StatusOK {
				t.Fatalf("%s: status %d", target, response.Code)
			}
			body := response.Body.String()
			if strings.Contains(body, `type="application/json"`) {
				t.Errorf("%s still embeds a JSON payload", target)
			}
			basis, _ := lookupAttr(find(t, body, "[data-explore]")[0], "data-basis")
			for _, attr := range []string{"data-shared-url", "data-basis-url", "data-other-basis-url"} {
				link := exploreDataLink(t, target, body, attr)
				key := basis + " " + attr
				if links[key] == nil {
					links[key] = map[string]string{}
				}
				if _, seen := links[key][link]; seen {
					continue
				}
				links[key][link] = target
				data := getExploreData(t, handler, target, body, attr)
				if data.Code != http.StatusOK || data.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" || data.Header().Get("Content-Type") != "application/json" {
					t.Errorf("%s %s: status %d, Cache-Control %q, Content-Type %q; want the linked version", target, link, data.Code, data.Header().Get("Cache-Control"), data.Header().Get("Content-Type"))
				}
			}
			for _, link := range find(t, body, `head link[rel="preload"]`) {
				if as, _ := lookupAttr(link, "as"); as != "fetch" {
					t.Errorf("%s preloads as %q", target, as)
				}
			}
			if preloads := len(find(t, body, `head link[rel="preload"]`)); preloads != 2 {
				t.Errorf("%s has %d preloads, want the shared and basis payloads", target, preloads)
			}
		}
	}
	for key, targets := range links {
		if len(targets) != 1 {
			t.Errorf("%s differs between selections: %v", key, targets)
		}
	}
}

func TestExploreDataCaching(t *testing.T) {
	archive := penaltyArchive(t, 20)
	handler := NewHandlerWithOptions(&archiveHTTPStore{archive: archive}, Options{ForecastIterations: 20})
	code, body := getPage(t, archive, "/nwsl-season/explore")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	link := exploreDataLink(t, "/nwsl-season/explore", body, "data-basis-url")
	if !strings.HasPrefix(link, "/nwsl-season/explore/data/all.json?v=") {
		t.Fatalf("basis link = %q", link)
	}
	current := getExploreData(t, handler, "/nwsl-season/explore", body, "data-basis-url")
	etag := current.Header().Get("ETag")
	if current.Code != http.StatusOK || etag == "" {
		t.Fatalf("status %d, ETag %q", current.Code, etag)
	}

	stale := httptest.NewRecorder()
	handler.ServeHTTP(stale, httptest.NewRequest(http.MethodGet, "/explore/data/all.json?v=stale", nil))
	if stale.Code != http.StatusOK || stale.Header().Get("Cache-Control") != "no-cache" || stale.Body.String() != current.Body.String() {
		t.Errorf("stale version: status %d, Cache-Control %q; want the current payload, revalidated", stale.Code, stale.Header().Get("Cache-Control"))
	}

	revalidated := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/explore/data/all.json", nil)
	request.Header.Set("If-None-Match", etag)
	handler.ServeHTTP(revalidated, request)
	if revalidated.Code != http.StatusNotModified || revalidated.Body.Len() != 0 {
		t.Errorf("revalidation: status %d, %d bytes; want 304", revalidated.Code, revalidated.Body.Len())
	}

	for _, target := range []string{"/explore/data/other.json", "/explore/data/all", "/explore/data/"} {
		missing := httptest.NewRecorder()
		handler.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, target, nil))
		if missing.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", target, missing.Code)
		}
	}
}
