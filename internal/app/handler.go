package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
)

// StatusReader reads cache freshness status.
type StatusReader interface {
	Status(context.Context) (cache.Status, error)
}

// NewHandler wires together the application's HTTP routes.
//
// It returns http.Handler rather than exposing a particular router. That keeps
// callers and tests independent from routing implementation details.
func NewHandler(statusReader ...StatusReader) http.Handler {
	mux := http.NewServeMux()
	var reader StatusReader
	if len(statusReader) > 0 {
		reader = statusReader[0]
	}
	mux.HandleFunc("GET /", home)
	mux.HandleFunc("GET /healthz", health)
	mux.HandleFunc("GET /cache/status", cacheStatus(reader))
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

func cacheStatus(reader StatusReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if reader == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(cacheStatusResponse{
				OK:    false,
				Error: "cache status unavailable",
			})
			return
		}

		status, err := reader.Status(r.Context())
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(cacheStatusResponse{
				OK:    false,
				Error: err.Error(),
			})
			return
		}

		response := cacheStatusResponse{OK: true}
		if status.LastAttempt != nil {
			response.LastAttempt = syncRunResponseFrom(status.LastAttempt)
		}
		if status.LastSuccess != nil {
			response.LastSuccess = syncRunResponseFrom(status.LastSuccess)
		}
		_ = json.NewEncoder(w).Encode(response)
	}
}

type cacheStatusResponse struct {
	OK          bool             `json:"ok"`
	Error       string           `json:"error,omitempty"`
	LastAttempt *syncRunResponse `json:"last_attempt,omitempty"`
	LastSuccess *syncRunResponse `json:"last_success,omitempty"`
}

type syncRunResponse struct {
	ID            int64  `json:"id"`
	StartedAt     string `json:"started_at"`
	FinishedAt    string `json:"finished_at"`
	Season        string `json:"season"`
	Stage         string `json:"stage"`
	Outcome       string `json:"outcome"`
	ErrorSummary  string `json:"error_summary,omitempty"`
	TeamsUpserted int    `json:"teams_upserted"`
	GamesUpserted int    `json:"games_upserted"`
	GamesDeleted  int    `json:"games_deleted"`
	GamesSeen     int    `json:"games_seen"`
}

func syncRunResponseFrom(run *cache.SyncRun) *syncRunResponse {
	return &syncRunResponse{
		ID:            run.ID,
		StartedAt:     run.StartedAt.UTC().Format(time.RFC3339),
		FinishedAt:    run.FinishedAt.UTC().Format(time.RFC3339),
		Season:        run.Season,
		Stage:         run.Stage,
		Outcome:       run.Outcome,
		ErrorSummary:  run.ErrorSummary,
		TeamsUpserted: run.TeamsUpserted,
		GamesUpserted: run.GamesUpserted,
		GamesDeleted:  run.GamesDeleted,
		GamesSeen:     run.GamesSeen,
	}
}
