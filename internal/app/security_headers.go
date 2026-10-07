package app

import (
	"net/http"
	"strings"
)

// contentSecurityPolicy is defense in depth behind html/template's contextual
// escaping. If a future template or script change lets untrusted text become
// markup, the browser still refuses to run inline or third-party script, load
// unexpected resources, submit forms elsewhere, or frame the site.
//
// Pages load only same-origin scripts and stylesheets, same-origin images plus
// ASA's club logos, and make no fetch requests. Inline style attributes remain
// allowed because templates position bars and markers from computed values;
// html/template's CSS escaper sanitizes those values. Adding a new resource
// origin or a browser request requires updating this policy deliberately.
var contentSecurityPolicy = strings.Join([]string{
	"default-src 'none'",
	"script-src 'self'",
	"style-src 'self'",
	"style-src-attr 'unsafe-inline'",
	"img-src 'self' " + clubLogoOrigin,
	"form-action 'self'",
	"base-uri 'none'",
	"frame-ancestors 'none'",
}, "; ")

// withSecurityHeaders sets browser hardening headers on every response,
// including errors, redirects, static assets, and operator endpoints. HSTS is
// left to the TLS-terminating proxy, which owns the deployment's HTTPS policy.
func withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Set("Content-Security-Policy", contentSecurityPolicy)
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("X-Frame-Options", "DENY")
		header.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		header.Set("Cross-Origin-Opener-Policy", "same-origin")
		header.Set("Cross-Origin-Resource-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}
