package app

import "strings"

// canonicalLink names the page search engines should index in place of a
// query-string variant: Path from the site root, and Relative from the
// request for when no public URL is configured.
type canonicalLink struct{ Path, Relative string }

// canonicalPage is a page that may name a canonical link; pages without
// query-string variants don't implement it.
type canonicalPage interface{ canonicalLink() canonicalLink }

// canonicalURL is the href for the page's <link rel="canonical">, or empty
// when the page names none. It is absolute when a public URL is configured.
func canonicalURL(publicURL string, page any) string {
	linker, ok := page.(canonicalPage)
	if !ok {
		return ""
	}
	link := linker.canonicalLink()
	if link.Path == "" {
		return ""
	}
	if publicURL != "" {
		return strings.TrimRight(publicURL, "/") + link.Path
	}
	return link.Relative
}
