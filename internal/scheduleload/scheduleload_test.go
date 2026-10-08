package scheduleload

import (
	"math"
	"strings"
	"testing"
	"time"
	_ "time/tzdata" // DST rows must not depend on the host's zoneinfo.

	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

const (
	tolerance = 1e-12
	day       = 24 * time.Hour
)

func near(got, want float64) bool { return math.Abs(got-want) <= tolerance }

var day0 = time.Date(2026, time.April, 4, 20, 0, 0, 0, time.UTC)

func game(id, home, away string, kickoff time.Time) standings.Game {
	return standings.Game{ID: id, Status: fixtures.PreMatchStatus, HomeTeamID: home, AwayTeamID: away, Kickoff: kickoff}
}

func TestRecoveryPressure(t *testing.T) {
	// Pressure is (6d - rest) / 1d between five and six days.
	for _, test := range []struct {
		name string
		rest time.Duration
		want float64
	}{
		{"zero rest", 0, 1},
		{"four days", 4 * day, 1},
		{"just below full: 5d-1ns", 5*day - 1, 1},
		{"exactly full: 5d", 5 * day, 1},
		// (6d - (5d+1ns)) / 1d = (86400e9-1)/86400e9
		{"just above full: 5d+1ns", 5*day + 1, float64(day-1) / float64(day)},
		// (144h - 132h) / 24h = 0.5
		{"five and a half days", 5*day + 12*time.Hour, 0.5},
		// (144h - 138h) / 24h = 0.25
		{"five days eighteen hours", 5*day + 18*time.Hour, 0.25},
		// (6d - (6d-1ns)) / 1d = 1ns / 86400e9
		{"just below start: 6d-1ns", 6*day - 1, 1 / float64(day)},
		{"exactly start: 6d", 6 * day, 0},
		{"just above start: 6d+1ns", 6*day + 1, 0},
		{"a week", 7 * day, 0},
		{"negative rest saturates", -time.Hour, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := RecoveryPressure(test.rest); !near(got, test.want) {
				t.Fatalf("RecoveryPressure(%v) = %v, want %v", test.rest, got, test.want)
			}
		})
	}
}

func TestCongestion(t *testing.T) {
	for _, test := range []struct {
		name string
		load Team
		want float64
	}{
		{"rested", Team{}, 0},
		// 0.04 * 1
		{"full recovery pressure", Team{Recovery: 1}, 0.04},
		// 0.04 * 0.5
		{"half recovery pressure", Team{Recovery: 0.5}, 0.02},
		// 0 + 0.075
		{"accumulated load only", Team{ThirdWithinNine: true}, 0.075},
		// 0.04 * 1 + 0.075
		{"both", Team{Recovery: 1, ThirdWithinNine: true}, 0.115},
		// 0.04 * 0.25 + 0.075
		{"quarter pressure with load", Team{Recovery: 0.25, ThirdWithinNine: true}, 0.085},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := Congestion(test.load); !near(got, test.want) {
				t.Fatalf("Congestion(%+v) = %v, want %v", test.load, got, test.want)
			}
		})
	}
}

func TestCalculate(t *testing.T) {
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	type want struct {
		home, away Team
	}
	for _, test := range []struct {
		name  string
		games []standings.Game
		want  map[string]want
	}{
		{
			name:  "first match of a season has no load",
			games: []standings.Game{game("g1", "A", "B", day0)},
			want:  map[string]want{"g1": {}},
		},
		{
			// A plays at day0 (first), day0+6d (rest 6d -> 0, only the
			// second match so no third), day0+9d (rest 3d -> 1; span from
			// the first match is exactly 9d, inside the inclusive window).
			name: "third match exactly nine days after the first",
			games: []standings.Game{
				game("g3", "A", "D", day0.Add(9*day)),
				game("g1", "A", "B", day0),
				game("g2", "A", "C", day0.Add(6*day)),
			},
			want: map[string]want{
				"g1": {},
				"g2": {home: Team{Recovery: 0}},
				"g3": {home: Team{Recovery: 1, ThirdWithinNine: true}},
			},
		},
		{
			// The third match is 1ns outside the window; rest 3d+1ns is
			// still full pressure.
			name: "third match just over nine days after the first",
			games: []standings.Game{
				game("g1", "A", "B", day0),
				game("g2", "A", "C", day0.Add(6*day)),
				game("g3", "A", "D", day0.Add(9*day+1)),
			},
			want: map[string]want{
				"g1": {},
				"g2": {home: Team{Recovery: 0}},
				"g3": {home: Team{Recovery: 1}},
			},
		},
		{
			// g2 rest 5d -> 1; g3-g1 = 9d-1ns, inside the window; g3 rest
			// 4d-1ns -> 1.
			name: "third match just inside nine days",
			games: []standings.Game{
				game("g1", "A", "B", day0),
				game("g2", "A", "C", day0.Add(5*day)),
				game("g3", "A", "D", day0.Add(9*day-1)),
			},
			want: map[string]want{
				"g1": {},
				"g2": {home: Team{Recovery: 1}},
				"g3": {home: Team{Recovery: 1, ThirdWithinNine: true}},
			},
		},
		{
			// A is away in g2: rest 5d12h -> (144h - 132h) / 24h = 0.5.
			name: "away appearance and fractional recovery",
			games: []standings.Game{
				game("g1", "A", "B", day0),
				game("g2", "C", "A", day0.Add(5*day+12*time.Hour)),
			},
			want: map[string]want{
				"g1": {},
				"g2": {away: Team{Recovery: 0.5}},
			},
		},
		{
			// Same team, same calendar day, eight hours apart: rest 8h -> 1.
			name: "two matches on the same day",
			games: []standings.Game{
				game("g1", "A", "B", day0.Add(-8*time.Hour)),
				game("g2", "A", "C", day0),
			},
			want: map[string]want{
				"g1": {},
				"g2": {home: Team{Recovery: 1}},
			},
		},
		{
			name: "different teams at the same instant are independent",
			games: []standings.Game{
				game("g1", "A", "B", day0),
				game("g2", "C", "D", day0),
			},
			want: map[string]want{"g1": {}, "g2": {}},
		},
		{
			// 19:00 EST Mar 7 is 00:00Z Mar 8; 19:00 EDT Mar 13 is 23:00Z
			// Mar 13. Wall clocks are six days apart but only 143h elapsed:
			// (144h - 143h) / 24h = 1/24.
			name: "spring forward shortens a six-day wall-clock gap",
			games: []standings.Game{
				game("g1", "A", "B", time.Date(2026, time.March, 7, 19, 0, 0, 0, newYork)),
				game("g2", "A", "C", time.Date(2026, time.March, 13, 19, 0, 0, 0, newYork)),
			},
			want: map[string]want{
				"g1": {},
				"g2": {home: Team{Recovery: 1.0 / 24}},
			},
		},
		{
			// 19:00 EDT Oct 31 is 23:00Z; 19:00 EST Nov 6 is 00:00Z Nov 7.
			// Six wall-clock days is 145h elapsed, above the 144h start -> 0.
			name: "fall back lengthens a six-day wall-clock gap",
			games: []standings.Game{
				game("g1", "A", "B", time.Date(2026, time.October, 31, 19, 0, 0, 0, newYork)),
				game("g2", "A", "C", time.Date(2026, time.November, 6, 19, 0, 0, 0, newYork)),
			},
			want: map[string]want{
				"g1": {},
				"g2": {home: Team{Recovery: 0}},
			},
		},
		{
			// An in-progress game is ignored even with a zero kickoff and
			// does not reset rest: g3 follows g1 by 6d -> 0.
			name: "other statuses are skipped",
			games: []standings.Game{
				game("g1", "A", "B", day0),
				{ID: "g2", Status: "InProgress", HomeTeamID: "A", AwayTeamID: "C"},
				game("g3", "A", "D", day0.Add(6*day)),
			},
			want: map[string]want{
				"g1": {},
				"g3": {home: Team{Recovery: 0}},
			},
		},
		{
			// Both teams rest 5d -> 1.
			name: "completed games count",
			games: []standings.Game{
				{ID: "g1", Status: standings.CompletedStatus, HomeTeamID: "A", AwayTeamID: "B", Kickoff: day0},
				game("g2", "B", "A", day0.Add(5*day)),
			},
			want: map[string]want{
				"g1": {},
				"g2": {home: Team{Recovery: 1}, away: Team{Recovery: 1}},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := Calculate(test.games)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(test.want) {
				t.Fatalf("loads for %d fixtures, want %d: %+v", len(got), len(test.want), got)
			}
			for id, want := range test.want {
				fixture, ok := got[id]
				if !ok {
					t.Fatalf("no load for %q: %+v", id, got)
				}
				for side, pair := range map[string][2]Team{"home": {fixture.Home, want.home}, "away": {fixture.Away, want.away}} {
					if !near(pair[0].Recovery, pair[1].Recovery) || pair[0].ThirdWithinNine != pair[1].ThirdWithinNine {
						t.Errorf("%s %s = %+v, want %+v", id, side, pair[0], pair[1])
					}
				}
			}
		})
	}
}

func TestCalculateRejectsInvalidSchedules(t *testing.T) {
	for _, test := range []struct {
		name    string
		games   []standings.Game
		message string
	}{
		{
			name:    "duplicate fixture ID",
			games:   []standings.Game{game("g1", "A", "B", day0), game("g1", "C", "D", day0)},
			message: `duplicate fixture "g1"`,
		},
		{
			name:    "duplicate ID even when the repeat would be skipped",
			games:   []standings.Game{game("g1", "A", "B", day0), {ID: "g1", Status: "InProgress"}},
			message: `duplicate fixture "g1"`,
		},
		{
			name:    "missing kickoff",
			games:   []standings.Game{game("g1", "A", "B", time.Time{})},
			message: `fixture "g1" has no kickoff`,
		},
		{
			name:    "one team in two fixtures at the same instant",
			games:   []standings.Game{game("g1", "A", "B", day0), game("g2", "C", "A", day0)},
			message: `team "A" has multiple fixtures at 2026-04-04T20:00:00Z`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := Calculate(test.games)
			if err == nil {
				t.Fatalf("Calculate succeeded: %+v", got)
			}
			if !strings.Contains(err.Error(), test.message) {
				t.Fatalf("error = %q, want it to contain %q", err, test.message)
			}
			if got != nil {
				t.Fatalf("loads = %+v on error, want nil", got)
			}
		})
	}
}
