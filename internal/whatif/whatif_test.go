package whatif

import (
	"reflect"
	"strings"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/standings"
)

func TestParseVersionedSelections(t *testing.T) {
	got, err := Parse("1", []string{"one:h", "two:d", "three:a"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Outcome{"one": HomeWin, "two": Draw, "three": AwayWin}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse() = %#v, want %#v", got, want)
	}
}

func TestParseRejectsUnknownVersionDuplicateAndInvalidOutcome(t *testing.T) {
	tests := []struct {
		name    string
		version string
		values  []string
		want    string
	}{
		{"version", "2", []string{"one:h"}, "unsupported"},
		{"duplicate", "1", []string{"one:h", "one:a"}, "duplicate"},
		{"outcome", "1", []string{"one:x"}, "invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Parse(test.version, test.values)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestApplyUsesCanonicalScoresWithoutMutatingInput(t *testing.T) {
	games := []standings.Game{
		{ID: "home", Status: "PreMatch", HomeTeamID: "a", AwayTeamID: "b"},
		{ID: "draw", Status: "PreMatch", HomeTeamID: "b", AwayTeamID: "c"},
		{ID: "away", Status: "PreMatch", HomeTeamID: "c", AwayTeamID: "a"},
	}
	projected, err := Apply(games, map[string]Outcome{"home": HomeWin, "draw": Draw, "away": AwayWin})
	if err != nil {
		t.Fatal(err)
	}
	want := [][2]int{{1, 0}, {0, 0}, {0, 1}}
	for index, game := range projected {
		if game.Status != standings.CompletedStatus || *game.HomeScore != want[index][0] || *game.AwayScore != want[index][1] {
			t.Fatalf("projected[%d] = %+v, want score %v", index, game, want[index])
		}
		if games[index].HomeScore != nil || games[index].AwayScore != nil {
			t.Fatal("Apply mutated its input")
		}
	}
}

func TestApplyRejectsCompletedOrUnknownGame(t *testing.T) {
	score := 1
	games := []standings.Game{{ID: "done", Status: standings.CompletedStatus, HomeScore: &score, AwayScore: &score}}
	if _, err := Apply(games, map[string]Outcome{"done": HomeWin}); err == nil {
		t.Fatal("Apply accepted a completed game")
	}
}
