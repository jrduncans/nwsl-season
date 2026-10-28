package forecaststate

import (
	"reflect"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/simulation"
)

func TestPlayoffStateRoundTripAndCopies(t *testing.T) {
	supported := func(id string) bool { return id == "model" || id == "other" }
	state, err := ParsePlayoffs("2", "model", "other", []string{"qf-2:b:b:a", "qf-1:d:c:d"}, supported, "model")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"qf-1:d:c:d", "qf-2:b:a:b"}
	if !reflect.DeepEqual(state.Values(), want) {
		t.Fatalf("values = %v", state.Values())
	}
	clone := state.WithPlayoffWinner("final", simulation.PostseasonMatch{Home: "d", Away: "b", Winner: "d"}).WithModel("other")
	if len(state.PlayoffWinners) != 2 || len(clone.PlayoffWinners) != 3 || clone.ComparisonModelID != "" {
		t.Fatal("state mutated or comparison not cleared")
	}
	roundtrip, err := ParsePlayoffs("2", "other", "", clone.Values(), supported, "model")
	if err != nil || !reflect.DeepEqual(roundtrip.Values(), clone.Values()) {
		t.Fatalf("roundtrip: %v", err)
	}
	if len(clone.WithoutPlayoffWinner("final").PlayoffWinners) != 2 || len(clone.PlayoffWinners) != 3 {
		t.Fatal("remove mutated source")
	}
	for _, tc := range []struct {
		version, model, comparison string
		values                     []string
	}{
		{"1", "model", "", []string{"q:a:a:b"}},
		{"2", "model", "model", nil},
		{"2", "unknown", "", nil},
		{"2", "model", "", []string{"q:a"}},
		{"2", "model", "", []string{"q:c:a:b"}},
		{"2", "model", "", []string{"q:a:a:a"}},
		{"2", "model", "", []string{"q:a:a:b", "q:b:a:b"}},
	} {
		if _, err := ParsePlayoffs(tc.version, tc.model, tc.comparison, tc.values, supported, "model"); err == nil {
			t.Fatalf("accepted %+v", tc)
		}
	}
}
