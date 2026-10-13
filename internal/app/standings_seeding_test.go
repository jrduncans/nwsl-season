package app

import (
	"testing"

	"github.com/jrduncans/nwsl-season/internal/standings"
)

func TestFinishedStandingsSeedAdvantages(t *testing.T) {
	table := []standings.TableRow{}
	for _, id := range []string{"a", "b", "c", "houston", "kc", "f", "g"} {
		table = append(table, standings.TableRow{Team: standings.Team{ID: id}})
	}
	for _, test := range []struct {
		season string
		want   []string
	}{
		{"2022", []string{"Bye · Home", "Bye · Home", "Home", "Home", "", "", ""}},
		{"2024", []string{"Home", "Home", "Home", "Home", "", "", ""}},
		{"2019", []string{"Home", "Home", "", "", "", "", ""}},
	} {
		rows := addPlayoffSeeding(tableViews(table, 6), table, test.season)
		for i, row := range rows {
			if row.SeedBadge != test.want[i] {
				t.Errorf("%s seed %d badge %q", test.season, i+1, row.SeedBadge)
			}
		}
	}
	table[0].TieBreak.Undetermined = true
	rows := addPlayoffSeeding(tableViews(table, 6), table, "2022")
	if rows[0].SeedBadge != "" {
		t.Fatal("unresolved rank received a badge")
	}
}
