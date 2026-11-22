package app

import (
	"net/http"
	"strings"

	"github.com/jrduncans/nwsl-season/internal/edgecache"
)

// Cache policies for responses whose handler sets no Cache-Control of its own.
//
// Pages read only the local SQLite cache, which changes when the scheduler
// syncs. Production's Cloudflare cache rule stores a page at the edge for
// s-maxage and keeps nothing it isn't told to; browsers always revalidate
// (max-age=0), which an edge hit answers without reaching the origin. A deploy
// purges the hostname, so a new version shows at once. When the server purges
// the edge cache by tag after a sync changes data (Options.EdgeCachePurge),
// pages stay at the edge much longer; otherwise expiry alone bounds how stale
// they get. See README "Runtime and deployment".
const (
	// Current and future seasons can change on every sync. Without purging,
	// the edge keeps them 15 minutes and may serve one stale copy while it
	// refetches.
	cacheControlCurrentPage = "public, max-age=0, s-maxage=900, stale-while-revalidate=3600"
	// With purging, a sync that changes data removes them, so expiry only
	// bounds staleness when a purge fails.
	cacheControlPurgedCurrentPage = "public, max-age=0, s-maxage=86400, stale-while-revalidate=3600"
	// Earlier seasons are complete and their data rarely changes.
	cacheControlPastSeasonPage       = "public, max-age=0, s-maxage=86400, stale-while-revalidate=86400"
	cacheControlPurgedPastSeasonPage = "public, max-age=0, s-maxage=604800, stale-while-revalidate=86400"
	// Errors, redirects, operator endpoints and anything else not listed:
	// stored nowhere, so a transient failure or data-dependent redirect is
	// never replayed.
	cacheControlNoStore = "no-store"
)

// pageCache says whether shared caches may keep a response, for how long, and
// which edge cache purges remove it.
type pageCache int

const (
	pageNotCached pageCache = iota
	// pageLive can change with current-season data.
	pageLive
	// pagePastSeason belongs to an earlier season.
	pagePastSeason
)

// cachePolicy turns a pageCache into response headers.
type cachePolicy struct {
	currentSeason string
	// purged reports that the server purges the edge cache by tag when a
	// sync changes data.
	purged bool
}

func (p cachePolicy) cacheControl(kind pageCache) string {
	switch {
	case kind == pageLive && p.purged:
		return cacheControlPurgedCurrentPage
	case kind == pageLive:
		return cacheControlCurrentPage
	case kind == pagePastSeason && p.purged:
		return cacheControlPurgedPastSeasonPage
	case kind == pagePastSeason:
		return cacheControlPastSeasonPage
	default:
		return cacheControlNoStore
	}
}

// cacheTags lists the edge cache tags whose purge removes a page of this kind.
// Cloudflare strips the header before the response reaches visitors.
func cacheTags(kind pageCache) string {
	switch kind {
	case pageLive:
		return edgecache.TagPage + "," + edgecache.TagLive
	case pagePastSeason:
		return edgecache.TagPage
	default:
		return ""
	}
}

// setHeaders gives a response the Cache-Control and Cache-Tag for kind. The
// sitemap, which isn't HTML, uses it to choose its kind itself.
func (p cachePolicy) setHeaders(header http.Header, kind pageCache) {
	header.Set("Cache-Control", p.cacheControl(kind))
	if tags := cacheTags(kind); tags != "" {
		header.Set("Cache-Tag", tags)
	}
}

// withCacheHeaders gives each response a Cache-Control header, plus Cache-Tag
// where it may be cached, unless its handler already chose a Cache-Control, as
// the static and Explore data handlers do for their versioned URLs.
func withCacheHeaders(policy cachePolicy, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(&cacheHeaderWriter{ResponseWriter: w, request: r, policy: policy}, r)
	})
}

type cacheHeaderWriter struct {
	http.ResponseWriter
	request     *http.Request
	policy      cachePolicy
	wroteHeader bool
}

func (w *cacheHeaderWriter) WriteHeader(status int) {
	if !w.wroteHeader {
		w.wroteHeader = true
		header := w.Header()
		if header.Get("Cache-Control") == "" {
			w.policy.setHeaders(header, pageCacheFor(w.request, status, header.Get("Content-Type"), w.policy.currentSeason))
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

func pageCacheFor(r *http.Request, status int, contentType, currentSeason string) pageCache {
	if (r.Method != http.MethodGet && r.Method != http.MethodHead) || status != http.StatusOK || !strings.HasPrefix(contentType, "text/html") {
		return pageNotCached
	}
	if season, ok := pathSeason(r.URL.Path); ok && isPastSeason(season, currentSeason) {
		return pagePastSeason
	}
	return pageLive
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
