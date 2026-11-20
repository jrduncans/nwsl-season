package app

import (
	"net/http"
	"strings"
)

// Cache policies for responses whose handler sets no Cache-Control of its own.
//
// Pages read only the local SQLite cache, which changes when the scheduler
// syncs, a few times a day. Production's Cloudflare cache rule stores a page
// at the edge for s-maxage and keeps nothing it isn't told to; browsers always
// revalidate (max-age=0), which an edge hit answers without reaching the
// origin. A deploy purges the hostname, so a new version shows at once. See
// README "Runtime and deployment".
const (
	// Current and future seasons can change on every sync, so the edge keeps
	// them 15 minutes and may serve one stale copy while it refetches.
	cacheControlCurrentPage = "public, max-age=0, s-maxage=900, stale-while-revalidate=3600"
	// Earlier seasons are complete and their data no longer changes.
	cacheControlPastSeasonPage = "public, max-age=0, s-maxage=86400, stale-while-revalidate=86400"
	// Errors, redirects, operator endpoints and anything else not listed:
	// stored nowhere, so a transient failure or data-dependent redirect is
	// never replayed.
	cacheControlNoStore = "no-store"
)

// withCacheHeaders gives each response a Cache-Control header unless its
// handler already chose one, as the static and Explore data handlers do for
// their versioned URLs.
func withCacheHeaders(currentSeason string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(&cacheHeaderWriter{ResponseWriter: w, request: r, currentSeason: currentSeason}, r)
	})
}

type cacheHeaderWriter struct {
	http.ResponseWriter
	request       *http.Request
	currentSeason string
	wroteHeader   bool
}

func (w *cacheHeaderWriter) WriteHeader(status int) {
	if !w.wroteHeader {
		w.wroteHeader = true
		header := w.Header()
		if header.Get("Cache-Control") == "" {
			header.Set("Cache-Control", cacheControlFor(w.request, status, header.Get("Content-Type"), w.currentSeason))
		}
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *cacheHeaderWriter) Write(body []byte) (int, error) {
	if !w.wroteHeader {
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", http.DetectContentType(body))
		}
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *cacheHeaderWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func cacheControlFor(r *http.Request, status int, contentType, currentSeason string) string {
	if (r.Method != http.MethodGet && r.Method != http.MethodHead) || status != http.StatusOK || !strings.HasPrefix(contentType, "text/html") {
		return cacheControlNoStore
	}
	if season, ok := pathSeason(r.URL.Path); ok && isPastSeason(season, currentSeason) {
		return cacheControlPastSeasonPage
	}
	return cacheControlCurrentPage
}

// pathSeason returns the season in a /seasons/{season}/... path, after any
// legacy base path.
func pathSeason(requestPath string) (string, bool) {
	if internal, ok := stripBasePath(requestPath); ok {
		requestPath = internal
	}
	rest, ok := strings.CutPrefix(requestPath, "/seasons/")
	if !ok {
		return "", false
	}
	season, _, _ := strings.Cut(rest, "/")
	return season, season != ""
}

// isPastSeason reports whether season is a four-digit year before the current
// season's.
func isPastSeason(season, currentSeason string) bool {
	if len(season) != 4 || len(currentSeason) != 4 {
		return false
	}
	for _, digit := range season + currentSeason {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return season < currentSeason
}
