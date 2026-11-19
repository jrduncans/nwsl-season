package app

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"

	"github.com/jrduncans/nwsl-season/internal/history"
)

// Explore's browser payloads are the same whatever the query string selects,
// so the page links them instead of embedding them. explore.js fetches them
// from /explore/data/{part}.json, where part is "shared" or a basis key. Each
// link carries its part's content version, so a browser or proxy may keep a
// versioned response for good. See docs/explore-guide.md "Browser payloads".

// exploreDataPath is the route's path, before the part name.
const exploreDataPath = "/explore/data/"

// exploreSharedData is every payload that carries both bases or none.
type exploreSharedData struct {
	H2H         exploreHeadToHeadPayload  `json:"h2h"`
	TeamSeasons exploreTeamSeasonsPayload `json:"teamSeasons"`
	Thresholds  []exploreThresholdRecord  `json:"thresholds"`
	Parity      []exploreParityRecord     `json:"parity"`
	// Views is the view registry (explore_views.go).
	Views []exploreViewGroup `json:"views"`
}

// exploreDataRequest selects a payload instead of the page. Part is "" for
// the page. SkipForecast builds the payload a SkipsForecast view links, with
// no forecast benchmarks. Version is the version the client asked for.
type exploreDataRequest struct {
	Part, Version string
	SkipForecast  bool
}

// exploreDataPart is one encoded payload and its content version.
type exploreDataPart struct {
	body    []byte
	version string
}

func encodeExploreData(value any) (exploreDataPart, error) {
	body, err := payload(value).MarshalJSON()
	if err != nil {
		return exploreDataPart{}, err
	}
	sum := sha256.Sum256(body)
	return exploreDataPart{body: body, version: hex.EncodeToString(sum[:6])}, nil
}

// exploreDataLinks are the page's versioned payload links, relative to the
// page. Other is empty when the selection has no other basis.
type exploreDataLinks struct {
	Shared, Basis, Other string
}

// exploreDataURL links one part from the page at fromPath.
func exploreDataURL(fromPath, part string, skipForecast bool, version string) string {
	query := url.Values{}
	if skipForecast {
		query.Set("benchmarks", "off")
	}
	query.Set("v", version)
	return relativeURL(fromPath, exploreDataPath+part+".json") + "?" + query.Encode()
}

// preloader is a page that names fetches for the browser to start while it
// parses the page.
type preloader interface{ preloadLinks() []string }

// preloads is the page's preload links, for the head template.
func preloads(page any) []string {
	if p, ok := page.(preloader); ok {
		return p.preloadLinks()
	}
	return nil
}

// preloadLinks are the payloads the page draws its charts from.
func (p explorePage) preloadLinks() []string {
	if p.Data.Shared == "" {
		return nil
	}
	return []string{p.Data.Shared, p.Data.Basis}
}

// exploreData serves one Explore payload. It builds the payload the page
// would link, so a current link's version matches and the response may be
// kept for good. Any other version gets the current payload, revalidated on
// every use.
func (a *application) exploreData(w http.ResponseWriter, r *http.Request) {
	part := r.PathValue("part")
	basis := history.AllShots
	switch part {
	case "shared.json", basisKey(history.AllShots) + ".json":
	case basisKey(history.NonPenalty) + ".json":
		basis = history.NonPenalty
	default:
		http.NotFound(w, r)
		return
	}
	request := exploreDataRequest{Part: strings.TrimSuffix(part, ".json"), Version: r.URL.Query().Get("v"), SkipForecast: r.URL.Query().Get("benchmarks") == "off"}
	store, ok := a.store.(historicalStore)
	if !ok {
		http.Error(w, "The local archive cannot provide historical scoring data.", http.StatusServiceUnavailable)
		return
	}
	inputs, err := store.HistoricalRegularSeasons(r.Context())
	if err != nil {
		http.Error(w, "Historical scoring data could not be loaded.", http.StatusInternalServerError)
		return
	}
	summaries, err := history.SummarizeScoringBasis(inputs, basis)
	if err != nil {
		http.Error(w, "Historical scoring data could not be summarized.", http.StatusInternalServerError)
		return
	}
	// Build from the default selection on this basis. The payloads don't
	// depend on the rest of the query (TestExploreDataMatchesPage).
	page := r.Clone(r.Context())
	page.URL.Path = "/explore"
	page.URL.RawQuery = ""
	if basis.ExcludesPenalties() {
		page.URL.RawQuery = url.Values{penaltiesParam: {penaltiesExclude}}.Encode()
	}
	a.renderExplore(w, page, summaries, inputs, basis, request)
}

// writeExploreData writes a payload with its cache policy: for good when the
// client asked for its version, and otherwise revalidated against it.
func writeExploreData(w http.ResponseWriter, r *http.Request, request exploreDataRequest, part exploreDataPart) {
	etag := `"` + part.version + `"`
	if request.Version == part.version {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(part.body)
}

// exploreBasisPayload is the basis-specific payload for one basis.
func exploreBasisPayload(basis history.ScoringBasis, summaries []history.SeasonScoring, teams exploreTeamsView) exploreBasisData {
	return exploreBasisData{
		DefaultTeamSeason: teams.DefaultTeamSeason,
		ChartData:         exploreChartRecords(summaries),
		TeamSeasons:       teams.TeamSeasons,
		HistoryContext:    exploreHistoryContextData(teams.TeamSeasons, basis),
		HomeAdvantage:     exploreHomeAdvantageRecords(history.HomeAdvantage(summaries)),
	}
}
