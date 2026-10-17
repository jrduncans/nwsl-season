//go:build e2e

package e2e

import (
	"encoding/json"
	"math"
	"testing"

	playwright "github.com/mxschmitt/playwright-go"

	"github.com/jrduncans/nwsl-season/internal/apptest"
)

// The tests in this file call window.NWSLGeometry, the pure functions in
// explore-geometry.js, with edge cases the seeded charts cannot reach: tied,
// all-zero and single-point populations, negative gaps, very large ranges and
// coincident points.

// geometryPage opens an Explore page, which loads explore-geometry.js, and
// returns it. The page's own data does not matter to these tests.
func geometryPage(t *testing.T) playwright.Page {
	t.Helper()
	base := exploreBase(t, apptest.ScenarioSingle)
	return explorePage(t, base, Desktop, "explore")
}

// callGeometry calls NWSLGeometry[name](...args) in the page and decodes the
// JSON result. The special argument {"$usable": [indexes]} becomes a predicate
// that is true for points whose index is listed.
func callGeometry(t *testing.T, page playwright.Page, name string, args []any, out any) {
	t.Helper()
	// Playwright cannot serialize Go structs, so the arguments travel as JSON.
	encoded, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("encode %s arguments: %v", name, err)
	}
	evalJSON(t, page, `(call) => {
		const args = JSON.parse(call.args).map((arg) => arg && typeof arg === 'object' && !Array.isArray(arg) && '$usable' in arg
			? (point) => arg.$usable.includes(point.index) : arg);
		const result = window.NWSLGeometry[call.name](...args);
		return result === undefined ? null : result;
	}`, map[string]any{"name": name, "args": string(encoded)}, out)
}

// near reports whether a and b agree to a relative 1e-9.
func near(a, b float64) bool {
	return math.Abs(a-b) <= 1e-9*math.Max(1, math.Max(math.Abs(a), math.Abs(b)))
}

// TestGeometry runs every geometry test against one loaded page: the functions
// are pure, so sharing the page saves a server and a browser context each.
func TestGeometry(t *testing.T) {
	t.Parallel()
	page := geometryPage(t)
	for _, tc := range []struct {
		name string
		run  func(*testing.T, playwright.Page)
	}{
		{"ExposesOneGlobal", testGeometryExposesOneGlobal},
		{"GapExtent", testGeometryGapExtent},
		{"TrendAxis", testGeometryTrendAxis},
		{"MarginAxis", testGeometryMarginAxis},
		{"RankingBars", testGeometryRankingBars},
		{"HeadToHead", testGeometryHeadToHead},
		{"HeadToHeadHitAndLabels", testGeometryHeadToHeadHitAndLabels},
		{"HeadToHeadExpected", testGeometryHeadToHeadExpected},
		{"Points", testGeometryPoints},
		{"PlotDomain", testGeometryPlotDomain},
		{"ScatterGroup", testGeometryScatterGroup},
		{"PointRadius", testGeometryPointRadius},
		{"LogoPlacement", testGeometryLogoPlacement},
		{"LayoutLogos", testGeometryLayoutLogos},
		{"StripRows", testGeometryStripRows},
	} {
		t.Run(tc.name, func(t *testing.T) { tc.run(t, page) })
	}
}

func testGeometryExposesOneGlobal(t *testing.T, page playwright.Page) {
	var names []string
	evalJSON(t, page, `() => Object.keys(window.NWSLGeometry).sort()`, nil, &names)
	want := []string{"gapExtent", "headToHeadGeometry", "headToHeadHit", "headToHeadLabels", "headToHeadValue", "layoutLogos", "logoPlacement", "marginAxis", "plotDomain", "pointRadius", "rankingBar", "rankingDomain", "scatterGroup", "stripRows", "trendAxis"}
	if len(names) != len(want) {
		t.Fatalf("NWSLGeometry exports %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("NWSLGeometry exports %v, want %v", names, want)
		}
	}
	var frozen bool
	evalJSON(t, page, `() => Object.isFrozen(window.NWSLGeometry)`, nil, &frozen)
	if !frozen {
		t.Error("NWSLGeometry is not frozen, so a page script could replace its functions")
	}
}

func testGeometryGapExtent(t *testing.T, page playwright.Page) {
	for _, tc := range []struct {
		name   string
		values []float64
		want   float64
	}{
		{"no values", []float64{}, 0.115},
		{"all zero", []float64{0, 0, 0}, 0.115},
		{"tied tiny gaps stay at the floor", []float64{0.01, -0.01, 0.01}, 0.115},
		{"single point", []float64{0.5}, 0.575},
		{"negative gap sets the extent", []float64{-2, 1}, 2.3},
		{"largest magnitude wins", []float64{0.4, -0.9, 0.2}, 0.9 * 1.15},
		{"very large range", []float64{1e9, -3e9}, 3.45e9},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got float64
			callGeometry(t, page, "gapExtent", []any{tc.values}, &got)
			if !near(got, tc.want) {
				t.Errorf("gapExtent(%v) = %v, want %v", tc.values, got, tc.want)
			}
		})
	}
}

func testGeometryTrendAxis(t *testing.T, page playwright.Page) {
	for _, tc := range []struct {
		name     string
		values   []float64
		signed   bool
		ticks    []float64
		decimals int
	}{
		// Chart.js once labelled this per-match Differential axis only at zero.
		{"per-match differential", []float64{-4, 3, 1.2, -0.7}, true, []float64{-6, -4, -2, 0, 2, 4, 6}, 0},
		{"integer goals keep whole steps", []float64{1, -1, 0}, true, []float64{-2, -1, 0, 1, 2}, 0},
		{"rolling differential", []float64{0.4, -0.9, 0.2}, true, []float64{-1.5, -1, -0.5, 0, 0.5, 1, 1.5}, 1},
		{"no values", []float64{}, true, []float64{-0.15, -0.1, -0.05, 0, 0.05, 0.1, 0.15}, 2},
		{"tiny tied gaps", []float64{0.01, -0.01}, true, []float64{-0.15, -0.1, -0.05, 0, 0.05, 0.1, 0.15}, 2},
		{"per-match goals", []float64{0, 5, 2}, false, []float64{0, 2, 4, 6}, 0},
		{"points per match", []float64{0, 1, 3}, false, []float64{0, 1, 2, 3, 4}, 0},
		{"rolling averages", []float64{0.8, 1.6, 2.1}, false, []float64{0, 1, 2, 3}, 0},
		{"unsigned floor", []float64{0.2, 0.4}, false, []float64{0, 0.5, 1, 1.5}, 1},
		{"large signed range", []float64{230, -40}, true, []float64{-300, -200, -100, 0, 100, 200, 300}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got struct {
				Min, Max, Step float64
				Decimals       int
				Ticks          []float64
			}
			callGeometry(t, page, "trendAxis", []any{tc.values, tc.signed}, &got)
			if len(got.Ticks) != len(tc.ticks) {
				t.Fatalf("trendAxis(%v, %v) ticks = %v, want %v", tc.values, tc.signed, got.Ticks, tc.ticks)
			}
			for i := range tc.ticks {
				if !near(got.Ticks[i], tc.ticks[i]) {
					t.Fatalf("trendAxis(%v, %v) ticks = %v, want %v", tc.values, tc.signed, got.Ticks, tc.ticks)
				}
			}
			if !near(got.Min, tc.ticks[0]) || !near(got.Max, tc.ticks[len(tc.ticks)-1]) {
				t.Errorf("trendAxis(%v, %v) bounds = [%v, %v], want the end ticks", tc.values, tc.signed, got.Min, got.Max)
			}
			if got.Decimals != tc.decimals {
				t.Errorf("trendAxis(%v, %v) decimals = %d, want %d", tc.values, tc.signed, got.Decimals, tc.decimals)
			}
			for _, value := range tc.values {
				if math.Abs(value)*1.15 > got.Max+1e-9 {
					t.Errorf("trendAxis(%v, %v) max %v leaves no headroom above %v", tc.values, tc.signed, got.Max, value)
				}
			}
		})
	}
}

func testGeometryPlotDomain(t *testing.T, page playwright.Page) {
	for _, tc := range []struct {
		name     string
		values   []float64
		signed   bool
		min, max float64
	}{
		{"spread", []float64{1, 3}, false, 0.9, 3.1},
		{"tied population gets a small range", []float64{2, 2, 2}, false, 1.95, 2.05},
		{"single point", []float64{3}, false, 2.925, 3.075},
		{"coincident points", []float64{1.5, 1.5}, false, 1.45, 1.55},
		{"all zero", []float64{0, 0}, false, 0, 0.05},
		{"all zero but signed", []float64{0, 0}, true, -0.05, 0.05},
		{"nonnegative measure stops at zero", []float64{0.02, 1}, false, 0, 1.049},
		{"signed measure goes below zero", []float64{0.02, 1}, true, -0.029, 1.049},
		{"negative gap", []float64{-1.5, 0.5}, true, -1.6, 0.6},
		{"all negative signed", []float64{-3, -1}, true, -3.1, -0.9},
		{"very large range", []float64{0, 1e9}, false, 0, 1.05e9},
		{"very large tied values", []float64{1e9, 1e9}, true, 1e9 - 2.5e7, 1e9 + 2.5e7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got struct{ Min, Max float64 }
			callGeometry(t, page, "plotDomain", []any{tc.values, tc.signed}, &got)
			if !near(got.Min, tc.min) || !near(got.Max, tc.max) {
				t.Errorf("plotDomain(%v, signed=%v) = [%v, %v], want [%v, %v]", tc.values, tc.signed, got.Min, got.Max, tc.min, tc.max)
			}
			if got.Max <= got.Min {
				t.Errorf("plotDomain(%v) = [%v, %v] has no extent", tc.values, got.Min, got.Max)
			}
		})
	}
}

type pixel struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type pixelArea struct {
	Left   float64 `json:"left"`
	Right  float64 `json:"right"`
	Top    float64 `json:"top"`
	Bottom float64 `json:"bottom"`
}

func testGeometryPointRadius(t *testing.T, page playwright.Page) {
	area := pixelArea{Left: 0, Right: 600, Top: 0, Bottom: 600}
	for _, tc := range []struct {
		name   string
		points []pixel
		want   float64
	}{
		{"no points", nil, 10},
		{"one point with room", []pixel{{300, 300}}, 10},
		{"one point at the plot edge", []pixel{{5, 300}}, 6},
		{"coincident points keep the minimum", []pixel{{300, 300}, {300, 300}}, 6},
		{"points 20px apart", []pixel{{200, 300}, {220, 300}}, 6},
		{"points 25px apart", []pixel{{200, 300}, {225, 300}}, 8},
		{"points 40px apart", []pixel{{200, 300}, {240, 300}}, 10},
		{"one close pair sets the size for every point", []pixel{{100, 100}, {500, 500}, {300, 300}, {300, 327}}, 9},
	} {
		t.Run(tc.name, func(t *testing.T) {
			points := tc.points
			if points == nil {
				points = []pixel{}
			}
			var got float64
			callGeometry(t, page, "pointRadius", []any{points, area}, &got)
			if got != tc.want {
				t.Errorf("pointRadius(%v) = %v, want %v", points, got, tc.want)
			}
		})
	}
}

type rect struct {
	Left   float64 `json:"left"`
	Top    float64 `json:"top"`
	Right  float64 `json:"right"`
	Bottom float64 `json:"bottom"`
}

func testGeometryLogoPlacement(t *testing.T, page playwright.Page) {
	area := pixelArea{Left: 0, Right: 600, Top: 0, Bottom: 600}
	const radius = 9 // The gap to the point is radius + 3.
	centre := pixel{300, 300}
	for _, tc := range []struct {
		name      string
		point     pixel
		size      float64
		area      pixelArea
		obstacles []pixel
		occupied  []rect
		want      *rect
	}{
		{"above the point when there is room", centre, 22, area, []pixel{centre}, []rect{}, &rect{289, 266, 311, 288}},
		{"below the point under the top edge", pixel{300, 10}, 22, area, []pixel{{300, 10}}, []rect{}, &rect{289, 22, 311, 44}},
		{"below the point when another logo is above", centre, 22, area, []pixel{centre}, []rect{{280, 262, 320, 292}}, &rect{289, 312, 311, 334}},
		{"beside the point when another point is above", centre, 22, area, []pixel{centre, {300, 270}}, []rect{}, &rect{312, 289, 334, 311}},
		{"no room in a tiny plot", pixel{15, 15}, 22, pixelArea{Left: 0, Right: 30, Top: 0, Bottom: 30}, []pixel{{15, 15}}, []rect{}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got *rect
			callGeometry(t, page, "logoPlacement", []any{tc.point, tc.size, tc.area, tc.obstacles, tc.occupied, radius}, &got)
			switch {
			case tc.want == nil && got != nil:
				t.Errorf("logoPlacement = %+v, want no placement", *got)
			case tc.want != nil && got == nil:
				t.Errorf("logoPlacement = none, want %+v", *tc.want)
			case tc.want != nil && *got != *tc.want:
				t.Errorf("logoPlacement = %+v, want %+v", *got, *tc.want)
			}
		})
	}
}

// logoPoint is a plotted point for layoutLogos: pixel position and series index.
type logoPoint struct {
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	Index int     `json:"index"`
}

// placedLogo is one entry of layoutLogos' result: the point and its logo box.
type placedLogo struct {
	Point logoPoint `json:"point"`
	Rect  rect      `json:"rect"`
}

func layoutLogos(t *testing.T, page playwright.Page, points []logoPoint, area pixelArea, radius float64, usable []int) []placedLogo {
	t.Helper()
	var placed []placedLogo
	callGeometry(t, page, "layoutLogos", []any{points, points, area, radius, map[string]any{"$usable": usable}}, &placed)
	return placed
}

// assertLogosClear checks the layout's invariants for every placed logo: it
// lies inside the plot, is separated from every other logo by the 2px padding,
// and keeps radius + 2px away from every point.
func assertLogosClear(t *testing.T, placed []placedLogo, points []logoPoint, area pixelArea, radius float64) {
	t.Helper()
	const padding = 2
	for i, logo := range placed {
		r := logo.Rect
		if r.Left < area.Left || r.Right > area.Right || r.Top < area.Top || r.Bottom > area.Bottom {
			t.Errorf("logo %d (point %d) at %+v leaves the plot %+v", i, logo.Point.Index, r, area)
		}
		if width, height := r.Right-r.Left, r.Bottom-r.Top; width != height || width < 22 || width > 48 {
			t.Errorf("logo %d is %v x %v, want a square from 22 to 48", i, width, height)
		}
		for j, other := range placed[i+1:] {
			o := other.Rect
			if r.Left < o.Right+padding && r.Right+padding > o.Left && r.Top < o.Bottom+padding && r.Bottom+padding > o.Top {
				t.Errorf("logos %d and %d are closer than %dpx: %+v and %+v", i, i+1+j, padding, r, o)
			}
		}
		for _, point := range points {
			if point.X >= r.Left-radius-padding && point.X <= r.Right+radius+padding &&
				point.Y >= r.Top-radius-padding && point.Y <= r.Bottom+radius+padding {
				t.Errorf("logo %d at %+v covers point %d at (%v, %v)", i, r, point.Index, point.X, point.Y)
			}
		}
	}
}

func testGeometryLayoutLogos(t *testing.T, page playwright.Page) {
	area := pixelArea{Left: 0, Right: 600, Top: 0, Bottom: 600}
	const radius = 9

	t.Run("a lone logo grows to a twentieth of the plot width", func(t *testing.T) {
		points := []logoPoint{{300, 300, 0}}
		placed := layoutLogos(t, page, points, area, radius, []int{0})
		if len(placed) != 1 {
			t.Fatalf("placed %d logos, want 1", len(placed))
		}
		if size := placed[0].Rect.Right - placed[0].Rect.Left; size != 30 {
			t.Errorf("logo size = %v, want 30", size)
		}
		assertLogosClear(t, placed, points, area, radius)
	})

	t.Run("growth stops at 48 on a large plot and at 22 on a small one", func(t *testing.T) {
		for _, tc := range []struct {
			width, want float64
		}{{2000, 48}, {200, 22}, {440, 22}, {460, 23}} {
			wide := pixelArea{Left: 0, Right: tc.width, Top: 0, Bottom: 600}
			points := []logoPoint{{tc.width / 2, 300, 0}}
			placed := layoutLogos(t, page, points, wide, radius, []int{0})
			if len(placed) != 1 {
				t.Fatalf("width %v: placed %d logos, want 1", tc.width, len(placed))
			}
			if size := placed[0].Rect.Right - placed[0].Rect.Left; size != tc.want {
				t.Errorf("width %v: logo size = %v, want %v", tc.width, size, tc.want)
			}
		}
	})

	t.Run("growth steps down two pixels at a time to the largest size that fits", func(t *testing.T) {
		// A wide, short plot. Above and below fit only logos up to 38px, and
		// a point on the left blocks every large logo there. A point to the
		// right blocks logos of 44px and more, so 42px is the largest size
		// that fits beside the point.
		wide := pixelArea{Left: 0, Right: 1000, Top: 0, Bottom: 100}
		subject := logoPoint{500, 50, 0}
		obstacles := []logoPoint{subject, {458, 50, 1}, {566, 50, 2}}
		var placed []placedLogo
		callGeometry(t, page, "layoutLogos", []any{[]logoPoint{subject}, obstacles, wide, radius, map[string]any{"$usable": []int{0}}}, &placed)
		if len(placed) != 1 {
			t.Fatalf("placed %d logos, want 1", len(placed))
		}
		if size := placed[0].Rect.Right - placed[0].Rect.Left; size != 42 {
			t.Errorf("logo size = %v, want 42 (rect %+v)", size, placed[0].Rect)
		}
		assertLogosClear(t, placed, obstacles, wide, radius)
	})

	t.Run("the most isolated point is placed first, ties by index", func(t *testing.T) {
		points := []logoPoint{{100, 100, 0}, {110, 100, 1}, {500, 500, 2}}
		placed := layoutLogos(t, page, points, area, radius, []int{0, 1, 2})
		if len(placed) != 3 {
			t.Fatalf("placed %d logos, want 3", len(placed))
		}
		for i, want := range []int{2, 0, 1} {
			if placed[i].Point.Index != want {
				t.Errorf("logo %d belongs to point %d, want %d", i, placed[i].Point.Index, want)
			}
		}
		assertLogosClear(t, placed, points, area, radius)
	})

	t.Run("points without a usable logo get none", func(t *testing.T) {
		points := []logoPoint{{100, 100, 0}, {400, 400, 1}}
		placed := layoutLogos(t, page, points, area, radius, []int{1})
		if len(placed) != 1 || placed[0].Point.Index != 1 {
			t.Errorf("placed = %+v, want only point 1", placed)
		}
		if placed := layoutLogos(t, page, points, area, radius, []int{}); len(placed) != 0 {
			t.Errorf("with no usable logos, placed = %+v", placed)
		}
	})

	t.Run("coincident points never share space", func(t *testing.T) {
		points := []logoPoint{{300, 300, 0}, {300, 300, 1}, {300, 300, 2}, {300, 300, 3}}
		placed := layoutLogos(t, page, points, area, radius, []int{0, 1, 2, 3})
		// Four logos cannot all sit beside one spot; the first two by index
		// get places and the others are dropped rather than overlapped.
		if len(placed) != 2 || placed[0].Point.Index != 0 || placed[1].Point.Index != 1 {
			t.Fatalf("placed %+v, want only points 0 and 1", placed)
		}
		assertLogosClear(t, placed, points, area, radius)
	})

	t.Run("a crowd drops logos instead of overlapping them", func(t *testing.T) {
		var points []logoPoint
		var usable []int
		for i := range 16 {
			points = append(points, logoPoint{X: 250 + float64(i%4)*14, Y: 250 + float64(i/4)*14, Index: i})
			usable = append(usable, i)
		}
		placed := layoutLogos(t, page, points, area, radius, usable)
		if len(placed) == 0 || len(placed) >= len(points) {
			t.Fatalf("placed %d logos for %d points, want some but not all", len(placed), len(points))
		}
		assertLogosClear(t, placed, points, area, radius)
	})

	t.Run("a point at the plot corner keeps its logo inside", func(t *testing.T) {
		points := []logoPoint{{3, 3, 0}, {597, 597, 1}, {597, 3, 2}, {3, 597, 3}}
		placed := layoutLogos(t, page, points, area, radius, []int{0, 1, 2, 3})
		if len(placed) != 4 {
			t.Fatalf("placed %d logos, want 4", len(placed))
		}
		assertLogosClear(t, placed, points, area, radius)
	})
}

type span struct {
	Left  float64 `json:"left"`
	Right float64 `json:"right"`
}

func testGeometryStripRows(t *testing.T, page playwright.Page) {
	for _, tc := range []struct {
		name  string
		spans []span
		gap   float64
		want  []int
	}{
		{"no spans", []span{}, 2, []int{}},
		{"apart spans share the first row", []span{{0, 10}, {20, 30}, {40, 50}}, 2, []int{0, 0, 0}},
		{"touching spans need the gap", []span{{0, 10}, {11, 21}}, 2, []int{0, 1}},
		{"exactly the gap apart share a row", []span{{0, 10}, {12, 22}}, 2, []int{0, 0}},
		{"coincident spans stack", []span{{5, 15}, {5, 15}, {5, 15}}, 0, []int{0, 1, 2}},
		{"a later span reuses the lowest free row", []span{{0, 10}, {5, 15}, {13, 23}}, 2, []int{0, 1, 0}},
		{"input order does not change the layout", []span{{13, 23}, {5, 15}, {0, 10}}, 2, []int{0, 1, 0}},
		{"a wide span blocks its row", []span{{0, 100}, {10, 20}, {30, 40}, {110, 120}}, 2, []int{0, 1, 1, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []int
			callGeometry(t, page, "stripRows", []any{tc.spans, tc.gap}, &got)
			if len(got) != len(tc.want) {
				t.Fatalf("stripRows(%v) = %v, want %v", tc.spans, got, tc.want)
			}
			for index := range got {
				if got[index] != tc.want[index] {
					t.Fatalf("stripRows(%v) = %v, want %v", tc.spans, got, tc.want)
				}
			}
		})
	}
}

func testGeometryRankingBars(t *testing.T, page playwright.Page) {
	// These rankingDomain cases and internal/app's TestExploreTeamSeasonsExtent
	// must change together: server and browser chart bounds must agree.
	for _, tc := range []struct {
		values   []float64
		signed   bool
		min, max float64
	}{
		{[]float64{}, false, 0, .5}, {[]float64{0, 0}, true, -.5, .5}, {[]float64{1, 2}, false, 0, 2.5}, {[]float64{-2, 1}, true, -2.5, 2.5}, {[]float64{2.5, 2.37}, false, 0, 3}, {[]float64{1e9}, false, 0, 2e9},
	} {
		var domain struct{ Min, Max float64 }
		callGeometry(t, page, "rankingDomain", []any{tc.values, tc.signed}, &domain)
		if !near(domain.Min, tc.min) || !near(domain.Max, tc.max) {
			t.Fatalf("domain %+v", domain)
		}
	}
	var bounded struct{ Min, Max float64 }
	callGeometry(t, page, "rankingDomain", []any{[]float64{3, 2.9}, false, true}, &bounded)
	if bounded.Min != 0 || bounded.Max != 3 {
		t.Fatalf("points bounds: %+v", bounded)
	}
	for _, tc := range []struct{ value, companion, min, max, zero, left, width, marker float64 }{
		{2, 3, 0, 4, 0, 0, 50, 75}, {-.5, .5, -1, 1, 50, 25, 25, 75}, {0, 0, -.115, .115, 50, 50, 0, 50},
	} {
		var got struct{ Zero, Left, Width, Marker float64 }
		callGeometry(t, page, "rankingBar", []any{tc.value, tc.companion, map[string]float64{"min": tc.min, "max": tc.max}}, &got)
		if !near(got.Zero, tc.zero) || !near(got.Left, tc.left) || !near(got.Width, tc.width) || !near(got.Marker, tc.marker) {
			t.Fatalf("geometry %+v: %+v", tc, got)
		}
	}
}

func testGeometryHeadToHead(t *testing.T, page playwright.Page) {
	for _, tc := range []struct {
		margins []int
		extent  float64
	}{
		{[]int{}, 1}, {[]int{0}, 1}, {[]int{1}, 1}, {[]int{-3, 2, 0}, 5}, {[]int{10}, 10}, {[]int{11}, 20},
	} {
		var got struct {
			Extent  float64
			Columns []struct{ X, Width, Y, Height, Center float64 }
			Zero    float64
		}
		callGeometry(t, page, "headToHeadGeometry", []any{tc.margins, 350}, &got)
		if got.Extent != tc.extent || len(got.Columns) != len(tc.margins) {
			t.Fatalf("geometry: %+v", got)
		}
		for i, c := range got.Columns {
			if c.X < 34 || c.X+c.Width > 344 || c.Width <= 0 || !near(c.Height, math.Abs(float64(tc.margins[i]))*98/tc.extent) {
				t.Fatalf("column: %+v", c)
			}
			if tc.margins[i] <= 0 && c.Y != got.Zero || tc.margins[i] > 0 && !near(c.Y+c.Height, got.Zero) {
				t.Fatal("orientation")
			}
		}
	}
	for _, n := range []int{1, 26, 40} {
		var got struct{ Columns []struct{ X, Width float64 } }
		callGeometry(t, page, "headToHeadGeometry", []any{make([]int, n), 350}, &got)
		for i, c := range got.Columns {
			if c.Width <= 0 || c.X+c.Width > 344 || i > 0 && c.X <= got.Columns[i-1].X+got.Columns[i-1].Width {
				t.Fatalf("%d columns must fit separately: %+v", n, got)
			}
		}
	}
}

func testGeometryHeadToHeadHitAndLabels(t *testing.T, page playwright.Page) {
	columns := []map[string]float64{{"center": 10}, {"center": 20}, {"center": 30}}
	area := pixelArea{Left: 0, Right: 40, Top: 0, Bottom: 200}
	for _, tc := range []struct {
		point pixel
		want  *int
	}{
		{pixel{0, 0}, playwright.Int(0)}, {pixel{40, 200}, playwright.Int(2)}, {pixel{15, 100}, playwright.Int(0)}, {pixel{16, 199}, playwright.Int(1)}, {pixel{25, 1}, playwright.Int(1)}, {pixel{-1, 100}, nil}, {pixel{10, 201}, nil},
	} {
		var got *int
		callGeometry(t, page, "headToHeadHit", []any{columns, area, tc.point}, &got)
		if (got == nil) != (tc.want == nil) || got != nil && *got != *tc.want {
			t.Fatalf("hit %+v = %v, want %v", tc.point, got, tc.want)
		}
	}
	var empty *int
	callGeometry(t, page, "headToHeadHit", []any{[]any{}, area, pixel{20, 100}}, &empty)
	if empty != nil {
		t.Fatal("empty hit")
	}
	for _, width := range []float64{350, 1000} {
		// First meetings 2022–2026 have only two slots between them at 390px.
		candidates := []map[string]any{}
		for i, center := range []float64{40, 100, 150, 200, 223, 246, 269, 292} {
			candidates = append(candidates, map[string]any{"text": string(rune('A' + i)), "center": center * width / 350, "width": 28})
		}
		var labels []struct {
			Text          string
			Center, Width float64
		}
		callGeometry(t, page, "headToHeadLabels", []any{candidates, pixelArea{Left: 34, Right: width - 6}, 8}, &labels)
		if len(labels) == 0 || width == 350 && len(labels) >= len(candidates) {
			t.Fatal("must retain readable labels and skip collisions")
		}
		if labels[0].Text != "A" || labels[len(labels)-1].Text != "H" {
			t.Fatalf("must retain edge years before intermediate labels: %+v", labels)
		}
		for i, label := range labels {
			if label.Center-label.Width/2 < 34 || label.Center+label.Width/2 > width-6 || i > 0 && label.Center-label.Width/2 < labels[i-1].Center+labels[i-1].Width/2+8 {
				t.Fatalf("overlap: %+v", labels)
			}
		}
	}
	for _, tc := range []struct {
		name       string
		candidates []map[string]any
		want       []string
	}{
		{"empty", []map[string]any{}, nil},
		{"one year", []map[string]any{{"text": "2026", "center": 5, "width": 28}}, []string{"2026"}},
		{"adjacent edge years", []map[string]any{{"text": "2016", "center": 5, "width": 28}, {"text": "2026", "center": 15, "width": 28}}, []string{"2016", "2026"}},
		{"edge years before middle", []map[string]any{{"text": "2016", "center": 5, "width": 28}, {"text": "2025", "center": 77, "width": 28}, {"text": "2026", "center": 95, "width": 28}}, []string{"2016", "2026"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var labels []struct {
				Text          string
				Center, Width float64
			}
			callGeometry(t, page, "headToHeadLabels", []any{tc.candidates, pixelArea{Left: 0, Right: 100}, 8}, &labels)
			if len(labels) != len(tc.want) {
				t.Fatalf("labels %+v, want %v", labels, tc.want)
			}
			for i, label := range labels {
				if label.Text != tc.want[i] || label.Center-label.Width/2 < 0 || label.Center+label.Width/2 > 100 || i > 0 && label.Center-label.Width/2 < labels[i-1].Center+labels[i-1].Width/2+8 {
					t.Fatalf("edge label: %+v", label)
				}
			}
		})
	}
}

func testGeometryHeadToHeadExpected(t *testing.T, page playwright.Page) {
	for _, tc := range []struct {
		goals, xg []any
		series    string
		want      *float64
	}{
		{[]any{2, 1}, []any{1.5, .5}, "actual", ptrFloat(1)},
		{[]any{2, 1}, []any{1.5, .5}, "both", ptrFloat(0)},
		{[]any{1, 0}, []any{2, .5}, "both", ptrFloat(-.5)},
		{[]any{1, 0}, []any{0, 0}, "both", ptrFloat(1)},
		{[]any{2, 1}, []any{nil, nil}, "actual", ptrFloat(1)},
		{[]any{2, 1}, []any{nil, nil}, "both", nil},
		{[]any{nil, nil}, []any{nil, nil}, "actual", nil},
		{[]any{2, 1}, []any{1.5, .5}, "expected", ptrFloat(1)},
		{[]any{nil, nil}, []any{.5, 2}, "expected", ptrFloat(-1.5)},
		{[]any{2, 1}, []any{nil, nil}, "expected", nil},
	} {
		var got *float64
		callGeometry(t, page, "headToHeadValue", []any{tc.goals, tc.xg, tc.series}, &got)
		if (got == nil) != (tc.want == nil) || got != nil && !near(*got, *tc.want) {
			t.Fatalf("gap/actual value: %v, want %v", got, tc.want)
		}
	}
	var bounds struct{ Extent float64 }
	callGeometry(t, page, "headToHeadGeometry", []any{[]any{nil, .4, -6.2}, 390, 260}, &bounds)
	if bounds.Extent != 10 {
		t.Fatalf("gap margins must set extent: %+v", bounds)
	}
}

func testGeometryPoints(t *testing.T, page playwright.Page) {
	for _, tc := range []struct {
		points  float64
		xpoints *float64
		want    *float64
	}{
		{3, ptrFloat(.4), ptrFloat(2.6)}, {0, ptrFloat(3), ptrFloat(-3)},
		{1, ptrFloat(2.6), ptrFloat(-1.6)}, {1, ptrFloat(.4), ptrFloat(.6)},
		{3, ptrFloat(3), ptrFloat(0)}, {3, ptrFloat(0), ptrFloat(3)}, {1, nil, nil},
	} {
		var got *float64
		callGeometry(t, page, "headToHeadValue", []any{[]any{nil, nil}, []any{nil, nil}, "points", tc.points, tc.xpoints}, &got)
		if (got == nil) != (tc.want == nil) || got != nil && !near(*got, *tc.want) {
			t.Fatalf("points gap = %v, want %v", got, tc.want)
		}
	}
	for _, values := range [][]any{{2.6}, {0}, {nil}, {-3, 3}, {}} {
		var bounds struct{ Extent float64 }
		callGeometry(t, page, "headToHeadGeometry", []any{values, 390, 260, "points"}, &bounds)
		if bounds.Extent != 3 {
			t.Fatalf("points extent = %v", bounds.Extent)
		}
	}
}

func ptrFloat(value float64) *float64 { return &value }

func testGeometryScatterGroup(t *testing.T, page playwright.Page) {
	var denseRadius float64
	callGeometry(t, page, "pointRadius", []any{[]map[string]float64{{"x": 50, "y": 50}, {"x": 50, "y": 50}}, map[string]float64{"left": 0, "right": 100, "top": 0, "bottom": 100}, 3, 6}, &denseRadius)
	if denseRadius != 3 {
		t.Fatalf("dense radius = %v, want 3", denseRadius)
	}

	points := []map[string]any{{"x": 1, "y": 2, "index": 0}, {"x": 1, "y": 2, "index": 1}, {"x": 1.2, "y": 2, "index": 2}, {"x": 1.4, "y": 2, "index": 3}}
	for _, tc := range []struct {
		index int
		want  []int
	}{{0, []int{0, 1, 2}}, {3, []int{2, 3}}, {4, nil}} {
		var got []int
		callGeometry(t, page, "scatterGroup", []any{points, tc.index}, &got)
		if len(got) != len(tc.want) {
			t.Fatalf("scatterGroup(%d) = %v, want %v", tc.index, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("scatterGroup(%d) = %v, want %v", tc.index, got, tc.want)
			}
		}
	}
	for _, tc := range []struct {
		values   []float64
		signed   bool
		min, max float64
	}{{[]float64{1, 3}, false, .5, 3.5}, {[]float64{-1.5, .5}, true, -2, 1}, {[]float64{0, 0}, false, 0, .05}} {
		var got struct{ Min, Max float64 }
		callGeometry(t, page, "plotDomain", []any{tc.values, tc.signed, true}, &got)
		if !near(got.Min, tc.min) || !near(got.Max, tc.max) {
			t.Fatalf("rounded domain = %+v, want %v–%v", got, tc.min, tc.max)
		}
	}
}

func testGeometryMarginAxis(t *testing.T, page playwright.Page) {
	for _, tc := range []struct {
		goals, xg []any
		extent    float64
	}{
		{[]any{4, -3, 0}, []any{.2, -.4, nil}, 6},
		{[]any{0, 1, -1}, []any{7.2, nil, -2.1}, 10},
		{[]any{0, 0}, []any{nil, nil}, 1},
		{[]any{nil}, []any{nil}, .15},
	} {
		var axis struct {
			Min, Max float64
			Ticks    []float64
		}
		callGeometry(t, page, "marginAxis", []any{tc.goals, tc.xg}, &axis)
		if !near(axis.Min, -tc.extent) || !near(axis.Max, tc.extent) || len(axis.Ticks) < 3 {
			t.Fatalf("shared margin axis: %+v, extent %v", axis, tc.extent)
		}
	}
}
