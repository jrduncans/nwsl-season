package app

import "strings"

// pageHead holds the <head> values render derives from the request. Every
// page embeds it, so render can fill it in through the page interface.
type pageHead struct {
	// CanonicalURL is the href for <link rel="canonical">. render sets it to
	// the page's own path with the query string dropped, so search engines
	// index one URL instead of every query-string variant. Error pages
	// leave it empty and name none.
	CanonicalURL string
	// Description is the page's meta description; see page_meta.go. Pages
	// without one, such as error pages, use siteDescription.
	Description string
}

func (h *pageHead) head() *pageHead { return h }

// page is a template value render can fill request-derived <head> values
// into: a pointer to a struct that embeds pageHead.
type page interface{ head() *pageHead }

// canonicalURL is the href naming requestPath, a path from the site root, as
// the canonical page. It is absolute when a public URL is configured and
// relative to the request otherwise.
func canonicalURL(publicURL, requestPath string) string {
	if publicURL != "" {
		return strings.TrimRight(publicURL, "/") + requestPath
	}
	return relativeURL(requestPath, requestPath)
}
