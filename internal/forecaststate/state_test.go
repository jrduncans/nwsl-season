package forecaststate

import (
	"reflect"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/simulation"
)

func TestParseV2KeepsLegacyURLsAndCanonicalValues(t *testing.T) {
	state, err := ParseV2("1", "results-poisson-v1", "", []string{"two:a", "one:h"}, func(id string) bool { return id == "results-poisson-v1" }, "results-poisson-v1")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := state.Values(), []string{"one:h", "two:a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("values = %#v, want %#v", got, want)
	}
}

func TestParseV2RejectsInvalidState(t *testing.T) {
	for _, test := range []struct {
		version, model string
		values         []string
	}{
		{"2", "", []string{"one:h"}},
		{"1", "other", []string{"one:h"}},
		{"1", "results-poisson-v1", []string{"one:x"}},
		{"1", "results-poisson-v1", []string{"one:h", "one:a"}},
	} {
		if _, err := ParseV2(test.version, test.model, "", test.values, func(id string) bool { return id == "results-poisson-v1" }, "results-poisson-v1"); err == nil {
			t.Fatalf("ParseV2(%q, %q, %#v) succeeded", test.version, test.model, test.values)
		}
	}
}

func TestWithAndWithoutDoNotMutateState(t *testing.T) {
	original := State{ModelID: "model", Fixed: map[string]simulation.Outcome{"one": simulation.HomeWin}}
	changed, err := original.With("two", simulation.Draw)
	if err != nil {
		t.Fatal(err)
	}
	if len(original.Fixed) != 1 || len(changed.Fixed) != 2 {
		t.Fatalf("original=%#v changed=%#v", original.Fixed, changed.Fixed)
	}
	without := changed.Without("one")
	if _, ok := without.Fixed["one"]; ok || len(changed.Fixed) != 2 {
		t.Fatalf("without=%#v changed=%#v", without.Fixed, changed.Fixed)
	}
}
