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

func TestGeometryExposesOneGlobal(t *testing.T) {
	page := geometryPage(t)
	var names []string
	evalJSON(t, page, `() => Object.keys(window.NWSLGeometry).sort()`, nil, &names)
	want := []string{"gapExtent", "layoutLogos", "logoPlacement", "plotDomain", "pointRadius"}
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

func TestGeometryGapExtent(t *testing.T) {
	page := geometryPage(t)
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

func TestGeometryPlotDomain(t *testing.T) {
	page := geometryPage(t)
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

func TestGeometryPointRadius(t *testing.T) {
	page := geometryPage(t)
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

func TestGeometryLogoPlacement(t *testing.T) {
	page := geometryPage(t)
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

func TestGeometryLayoutLogos(t *testing.T) {
	page := geometryPage(t)
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
		if len(placed) == 0 {
			t.Fatal("no logo was placed for the coincident points")
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
		if len(placed) == 0 || len(placed) > len(points) {
			t.Fatalf("placed %d logos for %d points", len(placed), len(points))
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
