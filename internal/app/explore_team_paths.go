package app

import (
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strings"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/history"
)

// exploreTeamSlug names a team in the Team profile views' paths, as in
// /explore/season-by-season/portland-thorns. A slug belongs to one ASA team ID,
// never to a club across IDs. Former keeps a renamed team's old slugs working:
// they redirect to Slug, which is the canonical one.
type exploreTeamSlug struct {
	Slug   string
	Former []string
}

// exploreTeamSlugs fixes each known team's slug, so a team ASA renames keeps
// its pages until someone chooses a new slug here and moves the old one to
// Former. A team missing here, such as a new expansion club, takes a slug
// derived from its newest name (deriveExploreTeamSlug).
var exploreTeamSlugs = map[string]exploreTeamSlug{
	"kRQa8JOqKZ": {Slug: "angel-city"},
	"315VnJ759x": {Slug: "bay-fc"},
	"4wM4Ezg5jB": {Slug: "boston-breakers"},
	"odMX2OJqYL": {Slug: "boston-legacy"},
	"KPqjw8PQ6v": {Slug: "chicago-stars"},
	"2lqRn34qr0": {Slug: "denver-summit"},
	"kRQaWa15KZ": {Slug: "fc-kansas-city"},
	"4JMAk47qKg": {Slug: "houston-dash"},
	"4wM4rZdqjB": {Slug: "kansas-city-current"},
	"raMyrr25d2": {Slug: "gotham-fc"},
	"zeQZeazqKw": {Slug: "north-carolina-courage"},
	"XVqKeVKM01": {Slug: "orlando-pride"},
	"Pk5LeeNqOW": {Slug: "portland-thorns"},
	"eV5DR6YQKn": {Slug: "racing-louisville"},
	"7VqG1lYMvW": {Slug: "san-diego-wave"},
	"7vQ7BBzqD1": {Slug: "seattle-reign"},
	"eV5D2w9QKn": {Slug: "utah-royals"},
	"aDQ0lzvQEv": {Slug: "washington-spirit"},
	"xW5pwDBMg1": {Slug: "western-new-york-flash"},
}

// exploreTeamPaths maps the archive's teams to and from their slugs.
type exploreTeamPaths struct {
	slugs  map[string]string // team ID → canonical slug
	ids    map[string]string // canonical slug → team ID
	former map[string]string // former slug → team ID
}

// newExploreTeamPaths assigns a slug to every team in the archive.
func newExploreTeamPaths(archive []cache.HistoricalSeason) exploreTeamPaths {
	names := map[string]string{}
	newest := map[string]string{}
	for _, season := range archive {
		// A team missing from the season's team list is named by its ID, as
		// Explore names it.
		for _, game := range season.Data.Games {
			for _, id := range []string{game.HomeTeamID, game.AwayTeamID} {
				if _, named := names[id]; !named && id != "" {
					names[id] = id
				}
			}
		}
		for _, team := range season.Data.Teams {
			if season.Entry.Season >= newest[team.ID] {
				names[team.ID], newest[team.ID] = displayName(team), season.Entry.Season
			}
		}
	}
	paths := exploreTeamPaths{slugs: map[string]string{}, ids: map[string]string{}, former: map[string]string{}}
	for id, slug := range exploreTeamSlugs {
		paths.slugs[id], paths.ids[slug.Slug] = slug.Slug, id
		for _, former := range slug.Former {
			paths.former[former] = id
		}
	}
	// Sorted, so colliding derived slugs number the same way on every request.
	for _, id := range slices.Sorted(maps.Keys(names)) {
		if _, fixed := paths.slugs[id]; fixed {
			continue
		}
		base := deriveExploreTeamSlug(names[id])
		slug := base
		for n := 2; paths.taken(slug); n++ {
			slug = fmt.Sprintf("%s-%d", base, n)
		}
		paths.slugs[id], paths.ids[slug] = slug, id
	}
	return paths
}

func (p exploreTeamPaths) taken(slug string) bool {
	_, current := p.ids[slug]
	_, former := p.former[slug]
	return current || former
}

// deriveExploreTeamSlug turns a team name into a slug: lowercase ASCII
// letters and digits joined by hyphens, without a trailing "FC".
func deriveExploreTeamSlug(name string) string {
	var words []string
	for word := range strings.FieldsFuncSeq(strings.ToLower(name), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	}) {
		words = append(words, word)
	}
	if len(words) > 1 && words[len(words)-1] == "fc" {
		words = words[:len(words)-1]
	}
	if len(words) == 0 {
		return "team"
	}
	return strings.Join(words, "-")
}

// slug is a team's canonical slug.
func (p exploreTeamPaths) slug(id string) (string, bool) {
	slug, ok := p.slugs[id]
	return slug, ok
}

// resolve finds the team a path segment names. current is false for a former
// slug, which redirects.
func (p exploreTeamPaths) resolve(segment string) (id string, current, ok bool) {
	if id, ok := p.ids[segment]; ok {
		return id, true, true
	}
	if id, ok := p.former[segment]; ok {
		return id, false, true
	}
	return "", false, false
}

// exploreTeamParams are the selections a team view can carry in its path, in
// path order: the team, then for Head to head the opponent.
var exploreTeamParams = []string{"team", "opponent"}

// exploreSelectionHref is the page and query for an Explore selection, from
// the site root. rawQuery holds the selection with its view as `view`, which
// the path names instead. A team view's team, and Head to head's opponent,
// move into the path when each is one team with a slug; the rest of the query
// keeps its order.
func exploreSelectionHref(rawQuery string, paths exploreTeamPaths) string {
	query, _ := url.ParseQuery(rawQuery)
	view, _ := exploreViewFor(exploreRequestedView(query))
	var target strings.Builder
	target.WriteString(exploreViewPath(view))
	rest := withoutQueryParameter(rawQuery, "view")
	for _, name := range exploreTeamParams[:view.TeamSegments] {
		slug, ok := paths.slug(query.Get(name))
		if len(query[name]) != 1 || !ok {
			break
		}
		target.WriteString("/" + url.PathEscape(slug))
		rest = withoutQueryParameter(rest, name)
	}
	if remaining, _ := url.ParseQuery(rest); view.Slug == "" && legacyTrendQuery(remaining) {
		// At bare /explore a lone metric would read as the legacy Scoring
		// trend link, and it means nothing to the default view.
		rest = withoutQueryParameter(rest, "metric")
	}
	if rest != "" {
		target.WriteString("?" + rest)
	}
	return target.String()
}

// exploreRedirectTarget is exploreSelectionHref relative to the page at
// fromPath, for a Location header: only a query string when the page stays
// the same.
func exploreRedirectTarget(fromPath, rawQuery string, paths exploreTeamPaths) string {
	path, rest, _ := strings.Cut(exploreSelectionHref(rawQuery, paths), "?")
	if path == fromPath && rest != "" {
		return "?" + rest
	}
	target := relativeURL(fromPath, path)
	if rest != "" {
		target += "?" + rest
	}
	return target
}

// exploreTeamPages lists the team pages search engines should find: each
// team view's page for every team in an official team-comparison season, and
// Head to head for every pair that has met, in both orders.
func exploreTeamPages(archive []cache.HistoricalSeason) ([]string, error) {
	summaries, err := history.SummarizeScoringBasis(archive, history.AllShots)
	if err != nil {
		return nil, err
	}
	paths := newExploreTeamPaths(archive)
	var teams []string
	for _, season := range summaries {
		if !season.TeamComparisonEligible() {
			continue
		}
		for _, team := range season.Teams {
			if !slices.Contains(teams, team.TeamID) {
				teams = append(teams, team.TeamID)
			}
		}
	}
	slices.SortFunc(teams, func(a, b string) int { return strings.Compare(paths.slugs[a], paths.slugs[b]) })
	var pages []string
	for _, group := range exploreViewGroups {
		for _, view := range group.Views {
			for _, team := range teams {
				switch view.TeamSegments {
				case 1:
					pages = append(pages, exploreSelectionHref(url.Values{"view": {view.Key}, "team": {team}}.Encode(), paths))
				case 2:
					for _, opponent := range history.HeadToHeadOpponentCounts(summaries, archive, team) {
						pages = append(pages, exploreSelectionHref(url.Values{"view": {view.Key}, "team": {team}, "opponent": {opponent.ID}}.Encode(), paths))
					}
				}
			}
		}
	}
	return pages, nil
}
