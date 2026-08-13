package main

import (
	"bytes"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/standings"
)

func TestPrintTableIncludesPointsPerGame(t *testing.T) {
	table := []standings.TableRow{
		{
			Team: standings.Team{Name: "Alpha FC"},
			Record: standings.Record{
				Played:       3,
				Wins:         2,
				Losses:       1,
				GoalsFor:     5,
				GoalsAgainst: 3,
				Points:       6,
			},
		},
		{
			Team: standings.Team{ID: "bravo"},
		},
	}

	var output bytes.Buffer
	if err := printTable(&output, table); err != nil {
		t.Fatal(err)
	}

	want := "#  Team      P  W  D  L  GF  GA  GD  Pts  PPG   TB\n" +
		"1  Alpha FC  3  2  0  1  5   3   2   6    2.00  \n" +
		"2  bravo     0  0  0  0  0   0   0   0    0.00  \n"
	if output.String() != want {
		t.Fatalf("printTable output:\n%s\nwant:\n%s", output.String(), want)
	}
}
