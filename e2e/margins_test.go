//go:build e2e

package e2e

import (
	"reflect"
	"testing"

	playwright "github.com/mxschmitt/playwright-go"

	"github.com/jrduncans/nwsl-season/internal/apptest"
)

// Compare server-rendered Go tiles with the in-place JS renderer from exactly
// the same archive, including every displayed line and the accessible record.
func TestExploreMarginsGoBrowserParity(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, scenario, path, tiles string
		missingSplit                bool
	}{
		{"season goal gaps", apptest.ScenarioSeasonTrend, "explore?view=season-trend&season=2025&team=odMX2OJqYL&trend-view=compare&trend-mode=match", "[data-trend-goal-gap-tiles]", false},
		{"season goal gaps incomplete", apptest.ScenarioSeasonTrend, "explore?view=season-trend&season=2026&team=odMX2OJqYL&trend-view=compare&trend-mode=match", "[data-trend-goal-gap-tiles]", false},
		{"h2h goal gaps", apptest.ScenarioTeamHistory, "explore?view=head-to-head&opponent=" + seattleH2H + "&h2h-series=both", "[data-h2h-comparison]", false},
		{"h2h goal gaps empty venue", apptest.ScenarioTeamHistory, "explore?view=head-to-head&opponent=aDQ0lzvQEv&h2h-series=both", "[data-h2h-comparison]", false},
		{"h2h goal gaps incomplete", apptest.ScenarioTeams, "explore?view=head-to-head&team=315VnJ759x&opponent=4JMAk47qKg&h2h-series=both", "[data-h2h-comparison]", false},
		{"h2h goal gaps missing split", apptest.ScenarioTeamHistory, "explore?view=head-to-head&opponent=" + seattleH2H + "&h2h-series=both&penalties=exclude", "[data-h2h-comparison]", true},
		{"season complete", apptest.ScenarioSeasonTrend, "explore?view=season-trend&season=2025&team=odMX2OJqYL&trend-view=difference&trend-mode=match", "[data-trend-margin-tiles]", false},
		{"season incomplete xG", apptest.ScenarioSeasonTrend, "explore?view=season-trend&season=2026&team=odMX2OJqYL&trend-view=difference&trend-mode=match", "[data-trend-margin-tiles]", false},
		{"season non-penalty", apptest.ScenarioSeasonTrend, "explore?view=season-trend&season=2025&team=odMX2OJqYL&trend-view=difference&trend-mode=match&penalties=exclude", "[data-trend-margin-tiles]", false},
		{"h2h complete", apptest.ScenarioTeamHistory, "explore?view=head-to-head&opponent=" + seattleH2H, "[data-h2h-record]", false},
		{"h2h incomplete xG", apptest.ScenarioTeams, "explore?view=head-to-head&team=315VnJ759x&opponent=4JMAk47qKg", "[data-h2h-record]", false},
		{"h2h non-penalty missing split", apptest.ScenarioTeamHistory, "explore?view=head-to-head&opponent=" + seattleH2H + "&penalties=exclude", "[data-h2h-record]", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			archive := apptest.Scenario(t, tc.scenario)
			if tc.missingSplit {
				for i := range archive {
					for j := range archive[i].Data.XGoals {
						archive[i].Data.XGoals[j].Penalties = nil
						archive[i].Data.XGoals[j].PenaltyRawJSON = ""
					}
				}
			}
			base := exploreBaseArchive(t, archive)
			noScript := newPageWith(t, pageOptions{Viewport: Mobile, NoScript: true})
			visitStatic(t, noScript, base+tc.path)
			read := `selector=>[...document.querySelector(selector).children].map(tile=>({lines:[...tile.children].map(line=>line.textContent.trim()),record:tile.querySelector('strong')?.getAttribute('aria-label') || ''}))`
			var want, got []struct {
				Lines  []string
				Record string
			}
			evalJSON(t, noScript, read, tc.tiles, &want)
			page := explorePage(t, base, Mobile, tc.path)
			evalJSON(t, page, read, tc.tiles, &got)
			if len(want) != 3 || !reflect.DeepEqual(got, want) {
				t.Fatalf("Go/JS tiles differ\nGo: %+v\nJS: %+v", want, got)
			}
			assertNoHorizontalOverflow(t, page)
			// Changing the basis must recompute from the same fixtures without a fetch.
			if tc.missingSplit {
				defer expectInPlace(t, page, "both bases of missing-split tiles")()
				chooseOption(t, page, "[data-h2h-penalties]", "include")
				chooseOption(t, page, "[data-h2h-penalties]", "exclude")
				evalJSON(t, page, read, tc.tiles, &got)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("basis toggle changed tiles: %+v != %+v", got, want)
				}
			}
		})
	}
}

func TestExploreMarginNumbersUseExplicitCoverage(t *testing.T) {
	t.Parallel()
	page := explorePage(t, exploreBase(t, apptest.ScenarioSeasonTrend), Desktop, "explore?view=season-trend")
	var totals []struct {
		Label                                                        string
		Played, Wins, Draws, Losses, Points, GoalsCovered, XGCovered int
		GoalsFor, GoalsAgainst, XGFor, XGAgainst                     float64
	}
	evalJSON(t, page, `()=>NWSLMargins.summarize([
 {venue:'Home',goalsCovered:true,values:[{actual:2,expected:1.25},{actual:0,expected:.5},{},{actual:3}]},
 {venue:'Away',goalsCovered:true,values:[{actual:0,expected:null},{actual:1,expected:null},{},{actual:0}]},
 {venue:'Home',goalsCovered:false,values:[{actual:4,expected:.5},{actual:0,expected:.75},{},{actual:3}]}
 ])`, nil, &totals)
	all := totals[0]
	if len(totals) != 3 || all.Played != 3 || all.Wins != 2 || all.Losses != 1 || all.Points != 6 || all.GoalsCovered != 2 || all.XGCovered != 2 || all.GoalsFor != 2 || all.GoalsAgainst != 1 || all.XGFor != 1.75 || all.XGAgainst != 1.25 {
		t.Fatalf("browser numeric totals: %+v", totals)
	}
}

func assertGoalGapPanels(t *testing.T, page playwright.Page, names []string) {
	t.Helper()
	var valid bool
	evalJSON(t, page, `(names)=>{
 const charts=names.map(name=>Chart.getChart(document.querySelector('[data-chart="'+name+'"]')));
 if(charts.some(c=>!c)) return false;
 const [a,b]=charts;
 return a.scales.y.min===b.scales.y.min && a.scales.y.max===b.scales.y.max && a.scales.y.min===-a.scales.y.max &&
 charts.every((c,side)=>c.data.datasets[0].data.every((v,i)=>{
  const r=c.meetings[i], actual=r.goalsCovered?r.values[side].actual:null, expected=r.values[side].expected;
  return actual===null||expected===null ? v===null : Math.abs(v-(actual-expected))<1e-9;
 })) && JSON.stringify(a.yearLabels)===JSON.stringify(b.yearLabels);
 }`, names, &valid)
	if !valid {
		t.Fatal("goal gap panels differ in values, axis or slots")
	}
	assertNoHorizontalOverflow(t, page)
}

func TestExploreGoalGapDisplayPrecision(t *testing.T) {
	t.Parallel()
	page := geometryPage(t)
	var valid bool
	evalJSON(t, page, `() => {
 const basis = {nonPenalty:false, xgLabel:'xG'};
 const cases = [[0,'matched expected'],[-1e-10,'matched expected'],[1e-10,'matched expected'],[-.0049,'matched expected'],[.0049,'matched expected'],[-.0051,'0.01 below'],[.0051,'0.01 above']];
 if (!cases.every(([gap,want]) => {
  const row = {venue:'Home', goalsCovered:true, values:[{actual:1,expected:1-gap},{actual:1,expected:1-gap},{},{actual:1}]};
  const tile = NWSLMargins.formatGoalGap(NWSLMargins.summarize([row])[0],basis,'match');
  return tile.scored.endsWith(' · '+want) && tile.allowed.endsWith(' · '+want);
 })) return false;
 return ['match','meeting'].every(unit => NWSLMargins.summarize([]).every(total => {
  const tile = NWSLMargins.formatGoalGap(total,basis,unit);
  return tile.scored===(unit==='match'?'No matches':'No meetings') && tile.allowed==='' && tile.coverage==='' && tile.splitCoverage==='';
 }));
 }`, nil, &valid)
	if !valid {
		t.Fatal("near-zero or empty-venue display regression")
	}
}
