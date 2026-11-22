package app

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/competition"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
)

// sitemapPath is where /robots.txt points crawlers for every season, stage
// and match page, so deep archive pages don't depend on link-following.
const sitemapPath = "/sitemap.xml"

type sitemapURLSet struct {
	XMLName xml.Name     `xml:"urlset"`
	XMLNS   string       `xml:"xmlns,attr"`
	URLs    []sitemapURL `xml:"url"`
}

type sitemapURL struct {
	Loc     string `xml:"loc"`
	LastMod string `xml:"lastmod,omitempty"`
}

// sitemap lists every page navigation links to, every match page, each
// Explore view's page and its team pages, by canonical URL. It needs absolute URLs, so it is a 404
// without a public URL.
func (a *application) sitemap(w http.ResponseWriter, r *http.Request) {
	if a.options.PublicURL == "" {
		http.NotFound(w, r)
		return
	}
	urls, err := a.sitemapURLs(r.Context())
	if err != nil {
		http.Error(w, "sitemap unavailable", http.StatusInternalServerError)
		return
	}
	body, err := xml.Marshal(sitemapURLSet{XMLNS: "http://www.sitemaps.org/schemas/sitemap/0.9", URLs: urls})
	if err != nil {
		http.Error(w, "sitemap unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	// Like a current-season page, it changes when a sync adds matches.
	a.cachePolicy().setHeaders(w.Header(), pageLive)
	_, _ = w.Write([]byte(xml.Header))
	_, _ = w.Write(body)
}

// sitemapURLs lists the pages of every public stage that has fixtures. A
// stage without any, such as playoffs not yet scheduled, would be an empty
// page; it joins the sitemap once a sync adds its matches.
func (a *application) sitemapURLs(ctx context.Context) ([]sitemapURL, error) {
	root := strings.TrimRight(a.options.PublicURL, "/")
	var urls []sitemapURL
	seen := map[string]bool{}
	add := func(path, lastMod string) {
		if !seen[path] {
			seen[path] = true
			urls = append(urls, sitemapURL{Loc: root + path, LastMod: lastMod})
		}
	}
	var latest time.Time
	for _, entry := range competition.PublicEntries() {
		scope := requestCompetition{Season: entry.Season, Stage: entry.Stage, Entry: entry, Cataloged: true, Valid: true}
		data, err := a.loadSeasonData(ctx, scope.Season, scope.Stage)
		if err != nil {
			return nil, fmt.Errorf("load %s %s: %w", scope.Season, scope.Stage, err)
		}
		if len(data.Games) == 0 {
			continue
		}
		presentation, _, err := a.stagePresentation(ctx, scope, data)
		if err != nil {
			return nil, fmt.Errorf("present %s %s: %w", scope.Season, scope.Stage, err)
		}
		rules, verified := a.rulesForSeason(scope.Season, scope.Stage)
		modified, dateOnly := a.stageLastModified(scope.Season, data)
		if modified.After(latest) {
			latest = modified
		}
		lastMod := sitemapDate(modified, dateOnly)
		for _, link := range stageLinks(scope, rules, verified, presentation) {
			add(link.path, lastMod)
		}
		if scope.forecastAvailable(rules, verified) {
			add(stageURL(scope.Season, scope.Entry.Slug)+"/model-evaluation", lastMod)
		}
		if matchPagesAvailable(scope) {
			for _, game := range data.Games {
				add(stageURL(scope.Season, scope.Entry.Slug)+"/matches/"+url.PathEscape(game.ASAID), lastMod)
			}
		}
	}
	for _, group := range exploreViewGroups {
		for _, view := range group.Views {
			add(exploreViewPath(view), sitemapDate(latest, false))
		}
	}
	if store, ok := a.store.(historicalStore); ok {
		archive, err := store.HistoricalRegularSeasons(ctx)
		if err != nil {
			return nil, fmt.Errorf("load the Explore archive: %w", err)
		}
		pages, err := exploreTeamPages(archive)
		if err != nil {
			return nil, fmt.Errorf("list Explore team pages: %w", err)
		}
		for _, page := range pages {
			add(page, sitemapDate(latest, false))
		}
	}
	return urls, nil
}

// stageLastModified is when a stage's pages last changed. A current season's
// pages change with each sync, so it is the last successful sync. An earlier
// season's data no longer changes, so it is the day of its last match
// (dateOnly), which a later sync of the same data doesn't move. It is zero
// when unknown.
func (a *application) stageLastModified(season string, data cache.SeasonData) (modified time.Time, dateOnly bool) {
	if isPastSeason(season, a.options.CurrentSeason) {
		for _, game := range data.Games {
			if kickoff, err := fixtures.ParseKickoff(game.KickoffUTC); err == nil && kickoff.After(modified) {
				modified = kickoff
			}
		}
		return modified, true
	}
	if data.LastSuccess == nil {
		return time.Time{}, false
	}
	return data.LastSuccess.FinishedAt, false
}

// sitemapDate formats a <lastmod>: a W3C date or date-time, or empty when
// unknown.
func sitemapDate(modified time.Time, dateOnly bool) string {
	switch {
	case modified.IsZero():
		return ""
	case dateOnly:
		return modified.UTC().Format(time.DateOnly)
	}
	return modified.UTC().Format(time.RFC3339)
}
