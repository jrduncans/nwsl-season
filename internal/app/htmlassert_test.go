package app

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// Scoped HTML assertions for handler tests.
//
// Instead of searching a whole response body for a substring, these helpers
// parse the page and check each expectation inside the element that should
// own it. A selector is a space-separated chain of compound selectors
// (descendant combinator). A compound selector is any combination of a tag
// name, #id, .class, [attr] and [attr=value] (value optionally quoted), for
// example `main section.forecast-comparison [data-total]`.

type selectorPart struct {
	tag, id string
	classes []string
	attrs   []attrMatch
}

type attrMatch struct {
	name, value string
	hasValue    bool
}

func parseSelector(t testing.TB, selector string) []selectorPart {
	t.Helper()
	var parts []selectorPart
	for _, compound := range strings.Fields(selector) {
		var part selectorPart
		for i := 0; i < len(compound); {
			switch compound[i] {
			case '#', '.':
				kind := compound[i]
				j := i + 1
				for j < len(compound) && !strings.ContainsRune("#.[", rune(compound[j])) {
					j++
				}
				name := compound[i+1 : j]
				if name == "" {
					t.Fatalf("selector %q: empty name after %q", selector, kind)
				}
				if kind == '#' {
					part.id = name
				} else {
					part.classes = append(part.classes, name)
				}
				i = j
			case '[':
				end := strings.IndexByte(compound[i:], ']')
				if end < 0 {
					t.Fatalf("selector %q: unterminated attribute", selector)
				}
				inner := compound[i+1 : i+end]
				match := attrMatch{name: inner}
				if name, value, ok := strings.Cut(inner, "="); ok {
					match = attrMatch{name: name, value: strings.Trim(value, `"'`), hasValue: true}
				}
				part.attrs = append(part.attrs, match)
				i += end + 1
			default:
				j := i
				for j < len(compound) && !strings.ContainsRune("#.[", rune(compound[j])) {
					j++
				}
				part.tag = compound[i:j]
				i = j
			}
		}
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		t.Fatalf("empty selector %q", selector)
	}
	return parts
}

func (p selectorPart) matches(n *html.Node) bool {
	if n.Type != html.ElementNode {
		return false
	}
	if p.tag != "" && n.Data != p.tag {
		return false
	}
	if p.id != "" && attr(n, "id") != p.id {
		return false
	}
	if len(p.classes) > 0 {
		have := strings.Fields(attr(n, "class"))
		for _, want := range p.classes {
			found := false
			for _, class := range have {
				found = found || class == want
			}
			if !found {
				return false
			}
		}
	}
	for _, want := range p.attrs {
		value, ok := lookupAttr(n, want.name)
		if !ok || (want.hasValue && value != want.value) {
			return false
		}
	}
	return true
}

func lookupAttr(n *html.Node, name string) (string, bool) {
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val, true
		}
	}
	return "", false
}

func attr(n *html.Node, name string) string {
	value, _ := lookupAttr(n, name)
	return value
}

// find returns every element in body matching selector, in document order.
func find(t testing.TB, body, selector string) []*html.Node {
	t.Helper()
	root, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}
	return findIn(t, root, selector)
}

// findIn is find within an already parsed subtree.
func findIn(t testing.TB, root *html.Node, selector string) []*html.Node {
	t.Helper()
	parts := parseSelector(t, selector)
	var out []*html.Node
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if parts[len(parts)-1].matches(n) && ancestorsMatch(n, parts[:len(parts)-1]) {
			out = append(out, n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return out
}

func ancestorsMatch(n *html.Node, parts []selectorPart) bool {
	for i := len(parts) - 1; i >= 0; i-- {
		n = n.Parent
		for n != nil && !parts[i].matches(n) {
			n = n.Parent
		}
		if n == nil {
			return false
		}
	}
	return true
}

// text returns the rendered text of n with runs of whitespace collapsed.
func text(n *html.Node) string {
	var b strings.Builder
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			b.WriteByte(' ')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(b.String()), " ")
}

// scopeText is the collapsed text of every element matching selector. It
// fails the test when nothing matches.
func scopeText(t testing.TB, body, selector string) string {
	t.Helper()
	nodes := find(t, body, selector)
	if len(nodes) == 0 {
		t.Errorf("no element matches %q", selector)
		return ""
	}
	texts := make([]string, len(nodes))
	for i, n := range nodes {
		texts[i] = text(n)
	}
	return strings.Join(texts, " ")
}

// requireText checks that each want appears in the text of the elements
// matching scope (use "body" for the whole page).
func requireText(t testing.TB, body, scope string, wants ...string) {
	t.Helper()
	got := scopeText(t, body, scope)
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("%s text missing %q; got %q", scope, want, got)
		}
	}
}

// forbidText checks that none of the unwanted strings appear in the text of
// the elements matching scope. A scope that matches nothing passes.
func forbidText(t testing.TB, body, scope string, unwanted ...string) {
	t.Helper()
	for _, n := range find(t, body, scope) {
		got := text(n)
		for _, bad := range unwanted {
			if strings.Contains(got, bad) {
				t.Errorf("%s text unexpectedly contains %q", scope, bad)
			}
		}
	}
}

// requireElements checks that every selector matches at least one element.
func requireElements(t testing.TB, body string, selectors ...string) {
	t.Helper()
	for _, selector := range selectors {
		if len(find(t, body, selector)) == 0 {
			t.Errorf("no element matches %q", selector)
		}
	}
}

// forbidElements checks that no selector matches any element.
func forbidElements(t testing.TB, body string, selectors ...string) {
	t.Helper()
	for _, selector := range selectors {
		if n := len(find(t, body, selector)); n > 0 {
			t.Errorf("%d element(s) unexpectedly match %q", n, selector)
		}
	}
}

// requireAttr checks that some element matching selector has attribute name
// equal to want.
func requireAttr(t testing.TB, body, selector, name, want string) {
	t.Helper()
	for _, n := range find(t, body, selector) {
		if attr(n, name) == want {
			return
		}
	}
	t.Errorf("no %q element has %s=%q", selector, name, want)
}

func TestHTMLAssertHelpers(t *testing.T) {
	const page = `<html><body><nav id="top" class="site-nav wide"><a href="a" data-x="1">Alpha</a></nav>
<main><section class="box"><h2>Title</h2><p>One   two
three</p><div data-flag><span class="k">k</span></div></section><section class="other"><a href="b">Beta</a></section></main></body></html>`
	cases := []struct {
		selector string
		want     int
	}{
		{"a", 2},
		{"#top", 1},
		{"nav.site-nav.wide", 1},
		{".site-nav a", 1},
		{"main a", 1},
		{"a[href=b]", 1},
		{`a[href="a"][data-x=1]`, 1},
		{"[data-flag]", 1},
		{"section.box .k", 1},
		{"section.other .k", 0},
		{"main nav", 0},
		{"p.missing", 0},
	}
	for _, tc := range cases {
		if got := len(find(t, page, tc.selector)); got != tc.want {
			t.Errorf("find(%q) = %d elements, want %d", tc.selector, got, tc.want)
		}
	}
	if got := scopeText(t, page, "section.box p"); got != "One two three" {
		t.Errorf("text = %q", got)
	}
	requireText(t, page, "section.box", "Title", "One two three")
	forbidText(t, page, "section.box", "Beta")
	forbidText(t, page, "section.nothing", "anything")
	requireElements(t, page, "nav", "[data-flag]")
	forbidElements(t, page, "table", "main nav")
	requireAttr(t, page, "a", "href", "b")
}
