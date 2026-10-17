//go:build e2e

package e2e

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/apptest"
	playwright "github.com/mxschmitt/playwright-go"
)

const seattleH2H = "7vQ7BBzqD1"

func assertHeadToHeadTiles(t *testing.T, page playwright.Page) {
	t.Helper()
	expect := playwright.NewPlaywrightAssertions()
	if err := expect.Locator(page.Locator("[data-h2h-tile]")).ToHaveCount(3); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator(".explore-h2h-legend, [data-h2h-record] table, [data-h2h-tile=All] i")).ToHaveCount(0); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-h2h-tile=Home] [data-h2h-encoding=home], [data-h2h-tile=Away] [data-h2h-encoding=away]")).ToHaveCount(2); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-h2h-direction]")).ToHaveText("up: win · zero: draw · down: loss"); err != nil {
		t.Fatal(err)
	}
	var fits bool
	evalJSON(t, page, `()=>{
 const tiles=[...document.querySelectorAll('[data-h2h-tile]')], first=tiles[0].getBoundingClientRect();
 return tiles.every(tile=>{const r=tile.getBoundingClientRect();return Math.abs(r.top-first.top)<1&&Math.abs(r.width-first.width)<1&&tile.scrollWidth<=tile.clientWidth});
 }`, nil, &fits)
	if !fits {
		t.Fatal("tiles must fit in three equal columns on one row")
	}
	var slack int
	evalJSON(t, page, `()=>Math.min(...[...document.querySelectorAll('[data-h2h-tile]')].map(tile=>{
 const widths=[...tile.children].map(line=>{
  const probe=line.cloneNode(true);probe.style.cssText='position:absolute;visibility:hidden;width:max-content;max-width:none';
  tile.append(probe);const width=probe.scrollWidth;probe.remove();return width;
 });
 return tile.clientWidth-Math.max(...widths);
 }))`, nil, &slack)
	if slack < 4 {
		t.Fatalf("tiles need at least 4px of line slack, got %dpx", slack)
	}
	var agrees bool
	evalJSON(t, page, `()=>[...document.querySelectorAll('[data-h2h-tile]')].every(tile=>{
 const rows=[...document.querySelectorAll('[data-h2h-rows] tr')].filter(row=>tile.dataset.h2hTile==='All'||row.children[2].textContent===tile.dataset.h2hTile), n=rows.length;
 const count=result=>rows.filter(row=>row.children[5].textContent===result).length;
 const goals=rows.reduce((sum,row)=>{row.children[4].textContent.split('–').map(Number).forEach((g,i)=>sum[i]+=g);return sum},[0,0]);
 const points=rows.reduce((sum,row)=>sum+Number(row.children[6].textContent),0);
 const expected=[tile.dataset.h2hTile,count('Win')+'–'+count('Draw')+'–'+count('Loss'),(n?(points/n).toFixed(2):'—')+' pts/match',goals.join('–')+' goals'];
 const covered=rows.filter(row=>!row.children[8].textContent.includes('Unavailable'));
 const xg=!n ? 'No matches' : covered.length===n ? [8,9].map(i=>covered.reduce((sum,row)=>sum+Number(row.children[i].textContent),0).toFixed(2)).join('–')+' xG' : 'xG incomplete';
 return [...tile.children].slice(0,4).every((child,i)=>child.textContent.trim()===expected[i])&&tile.textContent.includes(xg)&&tile.textContent.includes(n+' '+(n===1?'match':'matches'));
 })`, nil, &agrees)
	if !agrees {
		t.Fatal("server and in-place tiles must match official meeting rows")
	}
}

func TestExploreHeadToHeadPhoneLayout(t *testing.T) {
	t.Parallel()
	base := exploreBase(t, apptest.ScenarioTeamHistory)
	for _, width := range []int{320, 335, 360, 375, 390} {
		page := explorePage(t, base, viewport{Name: "phone-fold", Width: width, Height: 812}, "explore?view=head-to-head&opponent="+seattleH2H)
		assertHeadToHeadTiles(t, page)
		assertNoHorizontalOverflow(t, page)
		var visible bool
		evalJSON(t, page, `()=>{
 document.querySelector('.explore-h2h-controls').scrollIntoView({block:'start'});
 const tiles=document.querySelector('[data-h2h-record]').getBoundingClientRect(),chart=document.querySelector('[data-h2h-chart]').getBoundingClientRect();
 return tiles.top>=0 && tiles.bottom<=innerHeight && chart.width>0 && chart.height<=innerHeight;
 }`, nil, &visible)
		if width == 390 && !visible {
			t.Fatalf("%dpx: tiles must fit below controls and each panel must fit the viewport", width)
		}
	}
}

func TestExploreHeadToHeadInPlace(t *testing.T) {
	t.Parallel()
	expect := playwright.NewPlaywrightAssertions()
	base := exploreBase(t, apptest.ScenarioTeamHistory)
	page := explorePage(t, base, Mobile, "explore?view=head-to-head&opponent="+seattleH2H)
	defer expectInPlace(t, page, "head-to-head controls, sorts, Back/Forward")()
	if err := expect.Locator(page.Locator("[data-h2h-summary]")).ToHaveText("Table: all 26 meetings"); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-h2h-values]")).Not().ToHaveAttribute("open", ""); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-h2h-title] img[alt='']")).ToHaveCount(2); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-h2h-rows] img, [data-h2h-chart] a, [data-h2h-chart] button")).ToHaveCount(0); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-h2h-penalties]")).ToBeVisible(); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-h2h-mark]")).ToHaveCount(26); err != nil {
		t.Fatal(err)
	}
	assertHeadToHeadTiles(t, page)
	var matches bool
	evalJSON(t, page, `() => [...document.querySelectorAll('[data-h2h-mark]')].every(mark => {
  const row = [...document.querySelectorAll('[data-h2h-rows] tr')].find(row => row.dataset.matchId === mark.dataset.h2hMark);
  const score = row.children[4].textContent.split('–').map(Number);
  return score[0] - score[1] === Number(mark.dataset.margin) && mark.getAttribute('aria-label').includes(row.children[4].textContent) && mark.getAttribute('aria-label').includes(row.children[2].textContent);
 })`, nil, &matches)
	if !matches {
		t.Fatal("chart/table orientation or exact scores")
	}
	assertNoHorizontalOverflow(t, page)
	chooseOption(t, page, "[data-h2h-opponent]", "raMyrr25d2")
	if queryOf(t, page).Get("opponent") != "raMyrr25d2" {
		t.Fatal("opponent URL")
	}
	if err := expect.Locator(page.Locator("[data-h2h-title]")).ToContainText("Gotham FC"); err != nil {
		t.Fatal(err)
	}
	assertHeadToHeadTiles(t, page)
	if _, err := page.GoBack(); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-h2h-opponent]")).ToHaveValue(seattleH2H); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-h2h-mark]")).ToHaveCount(26); err != nil {
		t.Fatal(err)
	}
	if _, err := page.GoForward(); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-h2h-opponent]")).ToHaveValue("raMyrr25d2"); err != nil {
		t.Fatal(err)
	}
	if _, err := page.GoBack(); err != nil {
		t.Fatal(err)
	}
	if err := page.Locator("[data-h2h-summary]").Click(); err != nil {
		t.Fatal(err)
	}
	if err := page.Locator("[data-h2h-sort=margin]").Click(); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-h2h-values]")).ToHaveAttribute("open", ""); err != nil {
		t.Fatal(err)
	}
	if queryOf(t, page).Get("h2h-sort") != "margin" {
		t.Fatal("sort URL")
	}
	assertNoHorizontalOverflow(t, page)
	chooseOption(t, page, "[data-h2h-team]", "315VnJ759x")
	if queryOf(t, page).Has("opponent") {
		t.Fatal("team change must reset opponent")
	}
	if err := expect.Locator(page.Locator("[data-h2h-title]")).ToContainText("Bay FC"); err != nil {
		t.Fatal(err)
	}
	if _, err := page.GoBack(); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-h2h-team]")).ToHaveValue("Pk5LeeNqOW"); err != nil {
		t.Fatal(err)
	}
}

func TestExploreHeadToHeadNoScriptAndEmpty(t *testing.T) {
	t.Parallel()
	expect := playwright.NewPlaywrightAssertions()
	base := exploreBase(t, apptest.ScenarioTeamHistory)
	for _, vp := range []viewport{Desktop, Mobile, {Name: "small-phone", Width: 335, Height: 812}} {
		page := newPageWith(t, pageOptions{Viewport: vp, NoScript: true})
		visitStatic(t, page, base+"explore?view=head-to-head&opponent="+seattleH2H)
		if err := expect.Locator(page.Locator("[data-h2h-fallback] li")).ToHaveCount(26); err != nil {
			t.Fatal(err)
		}
		assertHeadToHeadTiles(t, page)
		if err := expect.Locator(page.Locator("[data-h2h-values]")).Not().ToHaveAttribute("open", ""); err != nil {
			t.Fatal(err)
		}
		assertNoHorizontalOverflow(t, page)
		if err := page.Locator("[data-h2h-summary]").Click(); err != nil {
			t.Fatal(err)
		}
		if err := page.Locator("[data-h2h-sort=venue]").Click(); err != nil {
			t.Fatal(err)
		}
		if err := expect.Locator(page.Locator("[data-h2h-values]")).ToHaveAttribute("open", ""); err != nil {
			t.Fatal(err)
		}
		assertNoHorizontalOverflow(t, page)
		chooseOption(t, page, "[data-h2h-opponent]", "raMyrr25d2")
		if err := page.Locator("[data-h2h-submit]").Click(); err != nil {
			t.Fatal(err)
		}
		if queryOf(t, page).Get("opponent") != "raMyrr25d2" {
			t.Fatal("GET opponent")
		}
		chooseOption(t, page, "[data-h2h-team]", "raMyrr25d2")
		if err := page.Locator("[data-h2h-team-submit]").Click(); err != nil {
			t.Fatal(err)
		}
		if queryOf(t, page).Has("opponent") {
			t.Fatal("GET team must reset opponent")
		}
	}
	for _, noScript := range []bool{false, true} {
		page := newPageWith(t, pageOptions{Viewport: Mobile, NoScript: noScript})
		path := base + "explore?view=head-to-head&opponent=315VnJ759x"
		if noScript {
			visitStatic(t, page, path)
		} else {
			visit(t, page, path)
		}
		if err := expect.Locator(page.Locator("[data-h2h-empty]")).ToHaveText("Portland Thorns FC and Bay FC have not met in a regular-season match."); err != nil {
			t.Fatal(err)
		}
		if err := expect.Locator(page.Locator("[data-h2h-opponent]")).ToHaveValue("315VnJ759x"); err != nil {
			t.Fatal(err)
		}
		if err := expect.Locator(page.Locator("[data-h2h-chart-wrap]")).ToBeHidden(); err != nil {
			t.Fatal(err)
		}
		assertNoHorizontalOverflow(t, page)
	}
}

func TestExploreHeadToHeadProfileTab(t *testing.T) {
	t.Parallel()
	page := explorePage(t, exploreBase(t, apptest.ScenarioTeamHistory), Desktop, "explore?view=team-history&penalties=exclude")
	defer expectInPlace(t, page, "Head to head profile tab")()
	if err := page.Locator("[data-view-choice=head-to-head]").Click(); err != nil {
		t.Fatal(err)
	}
	if err := playwright.NewPlaywrightAssertions().Locator(page.Locator("[data-panel=head-to-head]")).ToBeVisible(); err != nil {
		t.Fatal(err)
	}
	if queryOf(t, page).Get("view") != "head-to-head" {
		t.Fatal("tab URL")
	}
}

// Read Chart.js's actual tooltip content and box, not a parallel DOM tooltip.
func assertHeadToHeadTooltip(t *testing.T, page playwright.Page, index int) string {
	t.Helper()
	// Chart.js queues native events through requestAnimationFrame. Tap returns
	// before that frame, so wait for activation rather than reading the old,
	// dismissed tooltip. All content, placement and encoding checks still apply.
	if _, err := page.WaitForFunction(`index => {
 const c=Chart.getChart(document.querySelector('[data-chart="head-to-head"]'));
 return c.tooltip.opacity>0 && c.tooltip.dataPoints?.[0]?.dataIndex===index;
 }`, index); err != nil {

		t.Fatal(err)
	}
	var state struct {
		Text, Label                string
		Index                      int
		Visible, Adjacent, Encoded bool
	}
	evalJSON(t, page, `(index)=>{
 const c=Chart.getChart(document.querySelector('[data-chart="head-to-head"]')), tip=c.tooltip, bar=c.getDatasetMeta(0).data[index];
 const row=c.meetings[index],label=document.querySelectorAll('[data-h2h-mark]')[index];
 const text=[...tip.title,...tip.body.flatMap(b=>b.lines)].join(' · ');
 return {text,label:label.getAttribute('aria-label'), index:tip.dataPoints?.[0]?.dataIndex??-1, visible:tip.opacity>0,
 adjacent:Math.abs(tip.caretX-bar.x)<1 && tip.x>=0 && tip.x+tip.width<=c.width && Math.abs(tip.caretY-bar.y)<1,
 encoded:label.dataset.venue===row.venue&&label.dataset.encoding===(row.venue==='Home'?'solid-blue':'striped-orange')};
 }`, index, &state)
	if state.Index != index || !state.Visible || !state.Adjacent || !state.Encoded {
		t.Fatalf("tooltip state: %+v", state)
	}
	if state.Label != state.Text {
		t.Fatalf("tooltip %q != accessible label %q", state.Text, state.Label)
	}
	return state.Text
}

func TestExploreHeadToHeadTooltip(t *testing.T) {
	t.Parallel()
	base := exploreBase(t, apptest.ScenarioTeamHistory)
	page := newPageWith(t, pageOptions{Viewport: Mobile, Touch: true})
	visit(t, page, base+"explore?view=head-to-head&opponent="+seattleH2H)
	defer expectInPlace(t, page, "transient meeting tooltips")()
	canvas := page.Locator(`[data-chart="head-to-head"]`)
	if err := canvas.ScrollIntoViewIfNeeded(); err != nil {
		t.Fatal(err)
	}
	var position struct{ X, Y float64 }
	evalJSON(t, page, `()=>{const c=Chart.getChart(document.querySelector('[data-chart="head-to-head"]'));return {x:c.getDatasetMeta(0).data[0].x,y:c.chartArea.top+2}}`, nil, &position)
	// Hover near the top, far from the draw's zero-line mark.
	if err := canvas.Hover(playwright.LocatorHoverOptions{Position: &playwright.Position{X: position.X, Y: position.Y}}); err != nil {
		t.Fatal(err)
	}
	hover := assertHeadToHeadTooltip(t, page, 0)
	if err := canvas.Press("Escape"); err != nil {
		t.Fatal(err)
	}
	var closed bool
	evalJSON(t, page, `()=>{const c=Chart.getChart(document.querySelector('[data-chart="head-to-head"]'));return c.tooltip.opacity===0&&c.getActiveElements().length===0}`, nil, &closed)
	if !closed {
		t.Fatal("Escape leaves tooltip/highlight")
	}
	if err := canvas.Tap(playwright.LocatorTapOptions{Position: &playwright.Position{X: position.X, Y: position.Y}}); err != nil {
		t.Fatal(err)
	}
	if tap := assertHeadToHeadTooltip(t, page, 0); tap != hover {
		t.Fatal("tap tooltip differs")
	}
	if err := page.Locator("[data-h2h-scope]").Tap(); err != nil {
		t.Fatal(err)
	}
	evalJSON(t, page, `()=>Chart.getChart(document.querySelector('[data-chart="head-to-head"]')).tooltip.opacity===0`, nil, &closed)
	if !closed {
		t.Fatal("outside tap did not dismiss")
	}
	if err := canvas.Tap(playwright.LocatorTapOptions{Position: &playwright.Position{X: position.X, Y: position.Y}}); err != nil {
		t.Fatal(err)
	}
	if tap := assertHeadToHeadTooltip(t, page, 0); tap != hover {
		t.Fatal("same-column retap tooltip differs")
	}
	if err := canvas.Press("Escape"); err != nil {
		t.Fatal(err)
	}
	if err := canvas.Hover(playwright.LocatorHoverOptions{Position: &playwright.Position{X: position.X, Y: position.Y}}); err != nil {
		t.Fatal(err)
	}
	if nextHover := assertHeadToHeadTooltip(t, page, 0); nextHover != hover {
		t.Fatal("same-column hover after Escape differs")
	}
	if err := page.Locator("[data-h2h-summary]").Focus(); err != nil {
		t.Fatal(err)
	}
	if err := canvas.Focus(); err != nil {
		t.Fatal(err)
	}
	if keyboard := assertHeadToHeadTooltip(t, page, 0); keyboard != hover {
		t.Fatal("focus tooltip differs")
	}
	for _, move := range []struct {
		key   string
		index int
	}{{"ArrowRight", 1}, {"ArrowLeft", 0}, {"End", 25}, {"Home", 0}} {
		if err := canvas.Press(move.key); err != nil {
			t.Fatal(err)
		}
		assertHeadToHeadTooltip(t, page, move.index)
	}
	if err := canvas.Press("Escape"); err != nil {
		t.Fatal(err)
	}
	assertNoHorizontalOverflow(t, page)
	var labelsClear bool
	evalJSON(t, page, `()=>{const c=Chart.getChart(document.querySelector('[data-chart="head-to-head"]'));return c.yearLabels[0].text===c.meetings[0].season && c.yearLabels.at(-1).text===c.meetings.at(-1).season && c.yearLabels.every((l,i,a)=>i===0||l.center-l.width/2>=a[i-1].center+a[i-1].width/2+8)}`, nil, &labelsClear)
	if !labelsClear {
		t.Fatal("year labels overlap")
	}
}

func TestExploreHeadToHeadExpectedInPlace(t *testing.T) {
	t.Parallel()
	expect := playwright.NewPlaywrightAssertions()
	base := exploreBase(t, apptest.ScenarioTeamHistory)
	page := newPageWith(t, pageOptions{Viewport: Mobile, Touch: true})
	visit(t, page, base+"explore?view=head-to-head&opponent="+seattleH2H)
	defer expectInPlace(t, page, "series, penalties and history from shared payload")()
	chooseOption(t, page, "[data-h2h-series]", "both")
	if err := expect.Locator(page.Locator("[data-h2h-penalties]")).ToBeVisible(); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-h2h-title]")).ToContainText("goals vs xG"); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-h2h-values]")).Not().ToHaveAttribute("open", ""); err != nil {
		t.Fatal(err)
	}
	assertNoHorizontalOverflow(t, page)
	if err := expect.Locator(page.Locator("[data-h2h-record]")).ToBeHidden(); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-h2h-comparison]")).ToBeVisible(); err != nil {
		t.Fatal(err)
	}
	var gaps bool
	evalJSON(t, page, `()=>{const c=Chart.getChart(document.querySelector('[data-chart="head-to-head"]'));return c.data.datasets[0].data.every((value,i)=>Math.abs(value-(c.meetings[i].basisGoals[0]-c.meetings[i].xg[0]))<1e-9)&&!c.config.plugins.some(p=>p.id==='headToHeadExpected')}`, nil, &gaps)
	if !gaps {
		t.Fatal("comparison must show gap bars with no ticks")
	}
	canvas := page.Locator("[data-chart=head-to-head]")
	if err := canvas.ScrollIntoViewIfNeeded(); err != nil {
		t.Fatal(err)
	}
	var point struct{ X, Y float64 }
	evalJSON(t, page, `()=>{const c=Chart.getChart(document.querySelector('[data-chart="head-to-head"]'));return {x:c.getDatasetMeta(0).data[0].x,y:c.chartArea.top+2}}`, nil, &point)
	if err := canvas.Hover(playwright.LocatorHoverOptions{Position: &playwright.Position{X: point.X, Y: point.Y}}); err != nil {
		t.Fatal(err)
	}
	tooltip := assertHeadToHeadTooltip(t, page, 0)
	if !strings.Contains(tooltip, "xG for:") || !strings.Contains(tooltip, "Goals against − xG against:") || !strings.Contains(tooltip, "Goals for − xG for:") {
		t.Fatal(tooltip)
	}
	if err := page.Keyboard().Press("Escape"); err != nil {
		t.Fatal(err)
	}
	if err := canvas.Tap(playwright.LocatorTapOptions{Position: &playwright.Position{X: point.X, Y: point.Y}}); err != nil {
		t.Fatal(err)
	}
	if got := assertHeadToHeadTooltip(t, page, 0); got != tooltip {
		t.Fatal("tap expected tooltip differs")
	}
	if err := page.Locator("[data-h2h-summary]").Focus(); err != nil {
		t.Fatal(err)
	}
	if err := page.Locator("[data-chart=head-to-head]").Focus(); err != nil {
		t.Fatal(err)
	}
	if got := assertHeadToHeadTooltip(t, page, 0); got != tooltip {
		t.Fatal("keyboard expected tooltip differs")
	}
	chooseOption(t, page, "[data-h2h-penalties]", "exclude")
	if err := expect.Locator(page.Locator("[data-h2h-comparison]")).ToContainText("non-penalty goals"); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-h2h-mark]")).ToHaveCount(26); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-h2h-points-note]")).ToBeHidden(); err != nil {
		t.Fatal(err)
	}
	assertNoHorizontalOverflow(t, page)
	chooseOption(t, page, "[data-h2h-series]", "actual")
	if queryOf(t, page).Has("h2h-series") {
		t.Fatal("default series must be omitted")
	}
	if err := expect.Locator(page.Locator("[data-h2h-penalties]")).ToBeVisible(); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-h2h-record]")).ToBeVisible(); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-h2h-comparison]")).ToBeHidden(); err != nil {
		t.Fatal(err)
	}
	var actual bool
	evalJSON(t, page, `()=>{const c=Chart.getChart(document.querySelector('[data-chart="head-to-head"]'));return c.data.datasets[0].data.every((v,i)=>v===c.meetings[i].basisGoals[0]-c.meetings[i].basisGoals[1])}`, nil, &actual)
	if !actual {
		t.Fatal("Actual must use the non-penalty basis too")
	}
	if _, err := page.GoBack(); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-h2h-series]")).ToHaveValue("both"); err != nil {
		t.Fatal(err)
	}
	if _, err := page.GoBack(); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-h2h-penalties]")).ToHaveValue("include"); err != nil {
		t.Fatal(err)
	}
	if _, err := page.GoForward(); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-h2h-penalties]")).ToHaveValue("exclude"); err != nil {
		t.Fatal(err)
	}
}

func TestExploreHeadToHeadExpectedNoScriptAndCoverage(t *testing.T) {
	t.Parallel()
	expect := playwright.NewPlaywrightAssertions()
	base := exploreBase(t, apptest.ScenarioTeams)
	path := "explore?view=head-to-head&team=315VnJ759x&opponent=4JMAk47qKg&h2h-series=both"
	var serverTotals string
	for _, noScript := range []bool{true, false} {
		page := newPageWith(t, pageOptions{Viewport: Mobile, NoScript: noScript})
		if noScript {
			visitStatic(t, page, base+path)
		} else {
			visit(t, page, base+path)
		}
		if err := expect.Locator(page.Locator("[data-h2h-coverage]")).ToHaveText("1 meeting lacks xG."); err != nil {
			t.Fatal(err)
		}
		if err := expect.Locator(page.Locator("[data-h2h-penalties]")).ToBeVisible(); err != nil {
			t.Fatal(err)
		}
		if err := expect.Locator(page.Locator("[data-h2h-comparison]")).ToContainText("xG incomplete"); err != nil {
			t.Fatal(err)
		}
		if err := expect.Locator(page.Locator("[data-h2h-comparison]")).Not().ToContainText("xPts"); err != nil {
			t.Fatal(err)
		}
		if err := expect.Locator(page.Locator("[data-h2h-fallback]")).ToContainText("Unavailable"); err != nil {
			t.Fatal(err)
		}
		if err := expect.Locator(page.Locator("[data-h2h-comparison] [data-goal-gap-tile]")).ToHaveCount(3); err != nil {
			t.Fatal(err)
		}
		totals, err := page.Locator("[data-h2h-comparison]").TextContent()
		if err != nil {
			t.Fatal(err)
		}
		totals = strings.Join(strings.Fields(totals), " ")
		if noScript {
			serverTotals = totals
		} else if strings.ReplaceAll(totals, " ", "") != strings.ReplaceAll(serverTotals, " ", "") {
			t.Fatalf("server/client totals differ: %q vs %q", serverTotals, totals)
		}
		assertNoHorizontalOverflow(t, page)
		if !noScript {
			var missing int
			evalJSON(t, page, `()=>Chart.getChart(document.querySelector('[data-chart="head-to-head"]')).data.datasets[0].data.findIndex(value=>value===null)`, nil, &missing)
			if missing < 0 {
				t.Fatal("fixture must have a missing xG slot")
			}
			canvas := page.Locator("[data-chart=head-to-head]")
			if err := canvas.Press("Home"); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < missing; i++ {
				if err := canvas.Press("ArrowRight"); err != nil {
					t.Fatal(err)
				}
			}
			tooltip := assertHeadToHeadTooltip(t, page, missing)
			if !strings.Contains(tooltip, "xG for: Unavailable") || !strings.Contains(tooltip, "Goals for − xG for: Unavailable") {
				t.Fatal(tooltip)
			}
		}
		chooseOption(t, page, "[data-h2h-penalties]", "exclude")
		if noScript {
			if err := page.Locator("[data-h2h-submit]").Click(); err != nil {
				t.Fatal(err)
			}
		}
		if err := expect.Locator(page.Locator("[data-h2h-coverage]")).ToContainText("3 of 4 meetings"); err != nil {
			t.Fatal(err)
		}
		if err := expect.Locator(page.Locator("[data-h2h-rows] tr")).ToHaveCount(4); err != nil {
			t.Fatal(err)
		}
		chooseOption(t, page, "[data-h2h-series]", "actual")
		if noScript {
			if err := page.Locator("[data-h2h-submit]").Click(); err != nil {
				t.Fatal(err)
			}
		}
		if err := expect.Locator(page.Locator("[data-h2h-penalties]")).ToBeVisible(); err != nil {
			t.Fatal(err)
		}
		if err := expect.Locator(page.Locator("[data-h2h-record]")).ToBeVisible(); err != nil {
			t.Fatal(err)
		}
		if err := expect.Locator(page.Locator("[data-h2h-comparison]")).ToBeHidden(); err != nil {
			t.Fatal(err)
		}
		assertNoHorizontalOverflow(t, page)
	}
}

func TestExploreHeadToHeadMarginsAlias(t *testing.T) {
	t.Parallel()
	expect := playwright.NewPlaywrightAssertions()
	base := exploreBase(t, apptest.ScenarioTeamHistory)
	for _, noScript := range []bool{false, true} {
		page := newPageWith(t, pageOptions{Viewport: Mobile, NoScript: noScript})
		path := base + "explore?view=head-to-head&opponent=" + seattleH2H + "&h2h-series=expected"
		if noScript {
			visitStatic(t, page, path)
		} else {
			visit(t, page, path)
			defer expectInPlace(t, page, "Margins alias and views")()
		}
		choose := func(selector, value string) {
			chooseOption(t, page, selector, value)
			if noScript {
				if err := page.Locator("[data-h2h-submit]").Click(); err != nil {
					t.Fatal(err)
				}
			}
		}
		for _, penalties := range []string{"include", "exclude"} {
			choose("[data-h2h-penalties]", penalties)
			if err := expect.Locator(page.Locator("[data-h2h-record]")).ToBeVisible(); err != nil {
				t.Fatal(err)
			}
			if err := expect.Locator(page.Locator("[data-h2h-series]")).ToHaveValue("actual"); err != nil {
				t.Fatal(err)
			}
			if err := expect.Locator(page.Locator("[data-h2h-comparison]")).ToBeHidden(); err != nil {
				t.Fatal(err)
			}
			if err := expect.Locator(page.Locator("[data-h2h-fallback]")).ToContainText(regexp.MustCompile(`(?i)goal margin:`)); err != nil {
				t.Fatal(err)
			}
			assertNoHorizontalOverflow(t, page)
			if !noScript {
				if queryOf(t, page).Has("h2h-series") {
					t.Fatal("alias must canonicalize to default")
				}
				assertMarginPanels(t, page, "head-to-head", "head-to-head-xg")
				canvas := page.Locator("[data-chart=head-to-head]")
				if err := canvas.Press("Home"); err != nil {
					t.Fatal(err)
				}
				if tooltip := assertHeadToHeadTooltip(t, page, 0); !strings.Contains(tooltip, "xG margin:") {
					t.Fatal(tooltip)
				}
			}
		}
		choose("[data-h2h-series]", "both")
		if err := expect.Locator(page.Locator("[data-h2h-comparison]")).ToBeVisible(); err != nil {
			t.Fatal(err)
		}
		choose("[data-h2h-series]", "actual")
		if err := expect.Locator(page.Locator("[data-h2h-record]")).ToBeVisible(); err != nil {
			t.Fatal(err)
		}
		if !noScript {
			if _, err := page.GoBack(); err != nil {
				t.Fatal(err)
			}
			if err := expect.Locator(page.Locator("[data-h2h-series]")).ToHaveValue("both"); err != nil {
				t.Fatal(err)
			}
			if _, err := page.GoForward(); err != nil {
				t.Fatal(err)
			}
			assertMarginPanels(t, page, "head-to-head", "head-to-head-xg")
		}
	}
}

// The two panels must preserve all chronological slots, their x coordinates,
// year labels, a common rounded scale and both selected-basis measures.
func assertMarginPanels(t *testing.T, page playwright.Page, first, second string) {
	t.Helper()
	var agrees bool
	evalJSON(t, page, `(names)=>{
 const [a,b]=names.map(name=>Chart.getChart(document.querySelector('[data-chart="'+name+'"]')));
 return a.config.type==='bar' && b.config.type==='bar' && a.scales.y.min===b.scales.y.min && a.scales.y.max===b.scales.y.max && a.scales.y.min===-a.scales.y.max &&
 JSON.stringify(a.yearLabels)===JSON.stringify(b.yearLabels) &&
 a.data.datasets[0].data.every((value,i)=>value===NWSLMargins.goalMargin(a.meetings[i])) &&
 b.data.datasets[0].data.every((value,i)=>value===NWSLMargins.xgMargin(b.meetings[i])) &&
 a.getDatasetMeta(0).data.every((bar,i)=>Math.abs(bar.x-b.getDatasetMeta(0).data[i].x)<.1);
 }`, []string{first, second}, &agrees)
	if !agrees {
		t.Fatal("Margins panels must share a scale and chronological slots with correct values")
	}
}

// assertPointsChart checks the plotted population and tiles against the
// oriented per-match inputs, including missing slots and venue encodings.
func assertPointsChart(t *testing.T, page playwright.Page, selector, tiles string) {
	t.Helper()
	var agrees bool
	evalJSON(t, page, `({selector, tiles}) => {
 const c=Chart.getChart(document.querySelector(selector)), rows=c.meetings;
 const close=(a,b)=>Math.abs(a-b)<1e-8;
 if(c.scales.y.min!==-3||c.scales.y.max!==3||c.data.datasets.length!==1||c.config.type!=='bar') return false;
 if(!rows.every((r,i)=>r.xpoints==null?c.data.datasets[0].data[i]===null:close(c.data.datasets[0].data[i],r.points-r.xpoints))) return false;
 if(!rows.every((r,i)=>r.venue==='Home'?c.getDatasetMeta(0).data[i].options.backgroundColor==='#1F6FA8':c.getDatasetMeta(0).data[i].options.backgroundColor instanceof CanvasPattern)) return false;
 const nodes=[...document.querySelectorAll(tiles+' [data-points-tile]')], first=nodes[0].getBoundingClientRect();
 return nodes.length===3&&nodes.every(tile=>{
 const r=tile.getBoundingClientRect(), meetings=rows.filter(row=>tile.dataset.pointsTile==='All'||row.venue===tile.dataset.pointsTile), n=meetings.length;
 const covered=meetings.filter(row=>row.xpoints!=null), actual=meetings.reduce((s,r)=>s+r.points,0), expected=covered.reduce((s,r)=>s+r.xpoints,0);
 return Math.abs(r.top-first.top)<1&&Math.abs(r.width-first.width)<1&&tile.scrollWidth<=tile.clientWidth&&tile.textContent.includes(covered.length+' of '+n)&&tile.querySelector('strong').textContent.trim()===(n?actual+' vs '+(covered.length===n?expected.toFixed(2)+' xPts':'xPts incomplete'):'No meetings');
 });
 }`, map[string]any{"selector": selector, "tiles": tiles}, &agrees)
	if !agrees {
		t.Fatal("points bars and venue totals must agree with complete inputs on a fixed ±3 axis")
	}
	assertNoHorizontalOverflow(t, page)
	canvas := page.Locator(selector)
	if err := canvas.Focus(); err != nil {
		t.Fatal(err)
	}
	if err := canvas.Press("Home"); err != nil {
		t.Fatal(err)
	}
	var status string
	evalJSON(t, page, `()=>document.querySelector('[data-chart-status]').textContent`, nil, &status)
	for _, part := range []string{"Points:", "xPoints:", "Points − xPoints:", " · "} {
		if !strings.Contains(status, part) {
			t.Fatalf("keyboard description %q lacks %s", status, part)
		}
	}
	if err := canvas.Press("End"); err != nil {
		t.Fatal(err)
	}
	if err := canvas.Press("Escape"); err != nil {
		t.Fatal(err)
	}
}

func TestExploreHeadToHeadPoints(t *testing.T) {
	t.Parallel()
	base := exploreBase(t, apptest.ScenarioTeamHistory)
	expect := playwright.NewPlaywrightAssertions()
	for _, vp := range []viewport{Desktop, Mobile} {
		page := explorePage(t, base, vp, "explore?view=head-to-head&opponent="+seattleH2H+"&penalties=exclude")
		chooseOption(t, page, "[data-h2h-series]", "points")
		if err := page.WaitForURL("**h2h-series=points**"); err != nil {
			t.Fatal(err)
		}
		assertPointsChart(t, page, `[data-chart="head-to-head"]`, `[data-h2h-points-tiles]`)
		for _, selector := range []string{"[data-h2h-penalties-label]", "[data-h2h-points-note]", "[data-h2h-comparison]"} {
			if err := expect.Locator(page.Locator(selector)).ToBeHidden(); err != nil {
				t.Fatal(err)
			}
		}
		if queryOf(t, page).Get("h2h-series") != "points" {
			t.Fatal("URL lost points")
		}
		chooseOption(t, page, "[data-h2h-series]", "both")
		if err := expect.Locator(page.Locator("[data-h2h-points-tiles]")).ToBeHidden(); err != nil {
			t.Fatal(err)
		}
		if _, err := page.GoBack(); err != nil {
			t.Fatal(err)
		}
		if err := expect.Locator(page.Locator("[data-h2h-series]")).ToHaveValue("points"); err != nil {
			t.Fatal(err)
		}
		assertPointsChart(t, page, `[data-chart="head-to-head"]`, `[data-h2h-points-tiles]`)
		if _, err := page.Reload(); err != nil {
			t.Fatal(err)
		}
		if err := expect.Locator(page.Locator("[data-h2h-points-tiles]")).ToBeVisible(); err != nil {
			t.Fatal(err)
		}
	}
	page := newPageWith(t, pageOptions{Viewport: Mobile, NoScript: true})
	visitStatic(t, page, base+"explore?view=head-to-head&opponent="+seattleH2H+"&h2h-series=points")
	if err := expect.Locator(page.Locator("[data-h2h-fallback] li").First()).ToContainText("Points − xPoints:"); err != nil {
		t.Fatal(err)
	}
	if err := expect.Locator(page.Locator("[data-h2h-points-tiles]")).ToBeVisible(); err != nil {
		t.Fatal(err)
	}
}

func TestExplorePointsTileFormattingParity(t *testing.T) {
	t.Parallel()
	base := exploreBase(t, apptest.ScenarioTeamHistory)
	for _, path := range []string{
		"explore?view=head-to-head&h2h-series=points&opponent=" + seattleH2H,
		"explore?view=head-to-head&h2h-series=points&opponent=aDQ0lzvQEv",
		"explore?view=season-trend&season=2026&team=315VnJ759x&trend-view=points&trend-mode=match",
	} {
		var rendered [2][]string
		for i, noScript := range []bool{true, false} {
			page := newPageWith(t, pageOptions{Viewport: Mobile, NoScript: noScript})
			if noScript {
				visitStatic(t, page, base+path)
			} else {
				visit(t, page, base+path)
			}
			selector := "[data-h2h-points-tiles] [data-points-tile]"
			if strings.Contains(path, "season-trend") {
				selector = "[data-trend-points-tiles] [data-points-tile]"
			}
			values, err := page.Locator(selector).AllTextContents()
			if err != nil {
				t.Fatal(err)
			}
			for _, value := range values {
				rendered[i] = append(rendered[i], strings.Join(strings.Fields(value), ""))
			}
			if strings.Contains(path, "opponent=aDQ0lzvQEv") {
				if err := playwright.NewPlaywrightAssertions().Locator(page.Locator(selector + "[data-points-tile=Away] strong")).ToHaveText("No meetings"); err != nil {
					t.Fatal(err)
				}
			}
		}
		if !slices.Equal(rendered[0], rendered[1]) {
			t.Fatalf("server and browser tiles differ: %v vs %v", rendered[0], rendered[1])
		}
	}
}

func TestExploreHeadToHeadGoalGapPanels(t *testing.T) {
	t.Parallel()
	base := exploreBase(t, apptest.ScenarioTeamHistory)
	for _, vp := range []viewport{Desktop, Mobile} {
		page := explorePage(t, base, vp, "explore?view=head-to-head&opponent="+seattleH2H+"&h2h-series=both")
		defer expectInPlace(t, page, "goal gap panel switches")()
		assertGoalGapPanels(t, page, []string{"head-to-head", "head-to-head-xg"})
		chooseOption(t, page, "[data-h2h-penalties]", "exclude")
		assertGoalGapPanels(t, page, []string{"head-to-head", "head-to-head-xg"})
		chooseOption(t, page, "[data-h2h-series]", "actual")
		chooseOption(t, page, "[data-h2h-series]", "both")
		if err := page.Locator("[data-chart=head-to-head-xg]").Press("End"); err != nil {
			t.Fatal(err)
		}
		expect := playwright.NewPlaywrightAssertions()
		if err := expect.Locator(page.Locator("[data-chart-status]")).ToContainText("Non-penalty goals against − npxG against:"); err != nil {
			t.Fatal(err)
		}
	}
}
