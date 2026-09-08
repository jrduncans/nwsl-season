package app

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/history"
)

var historySeasonPattern = regexp.MustCompile(`^[0-9]{4}$`)

// historicalStore is deliberately optional: History is available only to
// cache implementations that can supply one coherent historical snapshot.
// It must not fall back to individual season reads.
type historicalStore interface {
	HistoricalRegularSeasons(context.Context) ([]cache.HistoricalSeason, error)
}

func (a *application) history(w http.ResponseWriter, r *http.Request) {
	location := relativeURL(r.URL.Path, "/history/scoring")
	if r.URL.RawQuery != "" {
		location += "?" + r.URL.RawQuery
	}
	redirectRelative(w, location, http.StatusSeeOther)
}

func (a *application) historyScoring(w http.ResponseWriter, r *http.Request) {
	store, ok := a.store.(historicalStore)
	if !ok {
		a.renderHistoryError(w, r, http.StatusServiceUnavailable, "History unavailable", "The local archive cannot provide historical scoring data.")
		return
	}

	inputs, err := store.HistoricalRegularSeasons(r.Context())
	if err != nil {
		a.renderHistoryError(w, r, http.StatusInternalServerError, "History unavailable", "Historical scoring data could not be loaded.")
		return
	}
	summaries, err := history.SummarizeScoring(inputs)
	if err != nil {
		a.renderHistoryError(w, r, http.StatusInternalServerError, "History unavailable", "Historical scoring data could not be summarized.")
		return
	}

	selected, metric, err := historySelectionState(r.URL, summaries)
	if err != nil {
		a.renderHistoryBadRequest(w, r, err)
		return
	}
	a.render(w, "history", historyPageForMetric(r.URL.Path, summaries, selected, metric))
}

func historySelection(requestURL *url.URL, summaries []history.SeasonScoring) (string, error) {
	selected, _, err := historySelectionState(requestURL, summaries)
	return selected, err
}

type historyMetric string

const (
	historyMetricGoals   historyMetric = "goals"
	historyMetricXG      historyMetric = "xg"
	historyMetricCompare historyMetric = "compare"
)

func historySelectionState(requestURL *url.URL, summaries []history.SeasonScoring) (string, historyMetric, error) {
	requested, present, err := historySeasonValues(requestURL.RawQuery)
	if err != nil {
		return "", historyMetricGoals, err
	}
	metric, err := historyMetricValue(requestURL.RawQuery)
	if err != nil {
		return "", historyMetricGoals, err
	}
	if present {
		if len(requested) != 1 || requested[0] == "" || !historySeasonPattern.MatchString(requested[0]) {
			return "", metric, fmt.Errorf("season must be one supported four-digit year")
		}
		for _, summary := range summaries {
			if summary.Season == requested[0] {
				return requested[0], metric, nil
			}
		}
		return "", metric, fmt.Errorf("season %s is not in the regular-season catalog", requested[0])
	}

	for index := len(summaries) - 1; index >= 0; index-- {
		if summaries[index].PlotEligible && summaries[index].Lifecycle == cache.SourceScopeCompleted {
			return summaries[index].Season, metric, nil
		}
	}
	for index := len(summaries) - 1; index >= 0; index-- {
		if summaries[index].PlotEligible && summaries[index].Lifecycle == cache.SourceScopeActive {
			return summaries[index].Season, metric, nil
		}
	}
	for index := len(summaries) - 1; index >= 0; index-- {
		if summaries[index].Played > 0 {
			return summaries[index].Season, metric, nil
		}
	}
	return "", metric, nil
}

func historyMetricValue(rawQuery string) (historyMetric, error) {
	values := []string{}
	for _, part := range strings.Split(rawQuery, "&") {
		key, value, hasValue := strings.Cut(part, "=")
		decoded, err := url.QueryUnescape(key)
		if err != nil || decoded != "metric" {
			continue
		}
		if !hasValue {
			values = append(values, "")
			continue
		}
		decodedValue, err := url.QueryUnescape(value)
		if err != nil {
			return historyMetricGoals, fmt.Errorf("metric must be goals, xg, or compare")
		}
		values = append(values, decodedValue)
	}
	if len(values) == 0 {
		return historyMetricGoals, nil
	}
	if len(values) != 1 {
		return historyMetricGoals, fmt.Errorf("metric must be one of goals, xg, or compare")
	}
	switch historyMetric(values[0]) {
	case historyMetricGoals:
		return historyMetricGoals, nil
	case historyMetricXG:
		return historyMetricXG, nil
	case historyMetricCompare:
		return historyMetricCompare, nil
	default:
		return historyMetricGoals, fmt.Errorf("metric must be one of goals, xg, or compare")
	}
}

// historySeasonValues decodes only the one recognized History state. Invalid
// or malformed unrelated fields must not make a valid History URL invalid.
func historySeasonValues(rawQuery string) ([]string, bool, error) {
	values := []string{}
	present := false
	for _, part := range strings.Split(rawQuery, "&") {
		key, value, hasValue := strings.Cut(part, "=")
		decoded, err := url.QueryUnescape(key)
		if err != nil || decoded != "season" {
			continue
		}
		present = true
		if !hasValue {
			values = append(values, "")
			continue
		}
		decodedValue, err := url.QueryUnescape(value)
		if err != nil {
			return nil, true, fmt.Errorf("season must be one supported four-digit year")
		}
		values = append(values, decodedValue)
	}
	return values, present, nil
}

func (a *application) renderHistoryBadRequest(w http.ResponseWriter, r *http.Request, err error) {
	a.renderHistoryError(w, r, http.StatusBadRequest, "Invalid history selection", err.Error())
}

func (a *application) renderHistoryError(w http.ResponseWriter, r *http.Request, status int, title, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	a.render(w, "error", errorPage{
		Title: title, Message: message,
		HomePath: relativeURL(r.URL.Path, "/"), StylesheetPath: relativeURL(r.URL.Path, "/static/site.css"), ScriptPath: relativeURL(r.URL.Path, "/static/standings.js"),
		Navigation: []navigationItem{
			{Label: "Seasons", Path: relativeURL(r.URL.Path, "/seasons")},
			{Label: "History", Path: relativeURL(r.URL.Path, "/history/scoring"), Current: true},
		},
		CatalogPage: true,
	})
}

func historyURL(fromPath, season string, metric historyMetric) string {
	target := &url.URL{Path: "/history/scoring"}
	if season != "" {
		query := url.Values{}
		query.Set("season", season)
		if metric != historyMetricGoals {
			query.Set("metric", string(metric))
		}
		target.RawQuery = query.Encode()
	} else if metric != historyMetricGoals {
		target.RawQuery = url.Values{"metric": []string{string(metric)}}.Encode()
	}
	result := relativeURL(fromPath, target.Path)
	if target.RawQuery != "" {
		result += "?" + target.RawQuery
	}
	return result
}

func exclusionText(exclusions []string) string {
	labels := make([]string, 0, len(exclusions))
	for _, exclusion := range exclusions {
		switch exclusion {
		case "source_unavailable":
			labels = append(labels, "source data unavailable")
		case "lifecycle_unknown":
			labels = append(labels, "season status unavailable")
		case "upcoming":
			labels = append(labels, "upcoming season")
		case "inventory_incomplete":
			labels = append(labels, "known fixture inventory incomplete")
		case "historical_results_incomplete":
			labels = append(labels, "historical results incomplete")
		case "invalid_completed_results":
			labels = append(labels, "invalid completed results")
		case "below_minimum_matches":
			labels = append(labels, fmt.Sprintf("fewer than %d completed, valid matches", history.MinimumSeasonMatches))
		}
	}
	return strings.Join(labels, "; ")
}
