package forecaststate

import (
	"reflect"
	"strings"
	"testing"
)

// FuzzParseV2 checks that ParseV2 never panics, that successful parses
// survive an encode/parse round trip, and that unsupported models are
// rejected.
func FuzzParseV2(f *testing.F) {
	const (
		recommended = "results-poisson-v1"
		second      = "xg-poisson-v1"
	)
	supported := func(id string) bool { return id == recommended || id == second }

	// Seeds mirror the cases in state_test.go plus comparison and edge forms.
	// Fixed results are one "|"-separated string because the fuzzer cannot
	// generate []string.
	for _, seed := range []struct{ version, model, comparison, values string }{
		{"", "", "", ""},
		{"1", recommended, "", "two:a|one:h"},
		{"2", recommended, "", ""},
		{"2", recommended, second, "one:h|two:d|three:a"},
		{"2", "", "", "one:h"},
		{"1", "other", "", "one:h"},
		{"1", recommended, "", "one:x"},
		{"1", recommended, "", "one:h|one:a"},
		{"2", recommended, recommended, ""},
		{"2", "other", "", ""},
		{"3", recommended, "", ""},
		{"2", recommended, "", ":h"},
		{"2", recommended, "", "a:b:c"},
	} {
		f.Add(seed.version, seed.model, seed.comparison, seed.values)
	}

	f.Fuzz(func(t *testing.T, version, modelID, comparisonID, joined string) {
		var values []string
		if joined != "" {
			values = strings.Split(joined, "|")
		}

		state, err := ParseV2(version, modelID, comparisonID, values, supported, recommended)

		if version == EncodingVersion && !supported(modelID) && err == nil {
			t.Fatalf("ParseV2 accepted unsupported model %q: %+v", modelID, state)
		}
		if version == LegacyEncodingVersion && modelID != recommended && err == nil {
			t.Fatalf("ParseV2 accepted legacy model %q: %+v", modelID, state)
		}
		if err != nil {
			return
		}

		if len(state.Fixed) > MaxFixed {
			t.Fatalf("state has %d fixed results, limit %d", len(state.Fixed), MaxFixed)
		}
		again, err := ParseV2(EncodingVersion, state.ModelID, state.ComparisonModelID, state.Values(), supported, recommended)
		if err != nil {
			t.Fatalf("re-parsing %+v failed: %v", state, err)
		}
		if !reflect.DeepEqual(state, again) {
			t.Fatalf("round trip changed state: %+v -> %+v", state, again)
		}
	})
}
