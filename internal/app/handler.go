package app

import (
	"fmt"
	"net/http"
)

// NewHandler wires together the application's HTTP routes.
//
// It returns http.Handler rather than exposing a particular router. That keeps
// callers and tests independent from routing implementation details.
func NewHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", home)
	mux.HandleFunc("GET /healthz", health)
	return mux
}

func home(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprint(w, `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>NWSL season explorer</title></head>
<body>
  <main>
    <h1>NWSL season explorer</h1>
    <p>The first small Go checkpoint is running.</p>
  </main>
</body>
</html>`)
}

func health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintln(w, "ok")
}
