package qualification

import (
	"database/sql"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/clinching"
	"github.com/jrduncans/nwsl-season/internal/competition"
)

func TestShouldRetryLegacyKickoffOrderBatch(t *testing.T) {
	snapshot := cache.QualificationSnapshot{Statuses: []cache.QualificationStatus{{Method: clinching.ProofIncompleteSchedule, Reason: "fixture kickoff order is invalid"}, {Method: clinching.ProofIncompleteSchedule, Reason: "fixture kickoff order is invalid"}}}
	games := []cache.Game{{ASAID: "g1", Status: "PreMatch", HomeTeamID: "a", AwayTeamID: "b", KickoffUTC: "2026-11-01 22:00:00 UTC"}, {ASAID: "g2", Status: "FullTime", HomeTeamID: "b", AwayTeamID: "a", KickoffUTC: "2026-10-01 22:00:00 UTC", HomeScore: sql.NullInt64{Valid: true}, AwayScore: sql.NullInt64{Valid: true}}}
	if !shouldRetryKickoffOrder(snapshot, games) {
		t.Fatal("expected legacy kickoff-order batch to be retried")
	}

	snapshot.Statuses[1].Achievement = competition.AchievementPlayoffs
	if !shouldRetryKickoffOrder(snapshot, games) {
		t.Fatal("achievement metadata should not affect retry detection")
	}
}

func TestShouldRetryComputeBudgetBatch(t *testing.T) {
	tests := []struct {
		name     string
		status   cache.QualificationStatus
		expected bool
	}{
		{name: "status proof exhausted", status: cache.QualificationStatus{Method: clinching.ProofComputeBudget}, expected: true},
		{name: "no-help proof exhausted", status: cache.QualificationStatus{Method: clinching.ProofCheapBound, NoHelp: clinching.NoHelpPath{State: clinching.NoHelpUnresolved, Reason: "calculation budget exhausted"}}, expected: true},
		{name: "other no-help limitation", status: cache.QualificationStatus{Method: clinching.ProofCheapBound, NoHelp: clinching.NoHelpPath{State: clinching.NoHelpUnresolved, Reason: "missing tiebreak data"}}, expected: false},
		{name: "completed no-help proof", status: cache.QualificationStatus{Method: clinching.ProofCheapBound, NoHelp: clinching.NoHelpPath{State: clinching.NoHelpGuaranteed}}, expected: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := shouldRetryComputeBudget(cache.QualificationSnapshot{Statuses: []cache.QualificationStatus{test.status}})
			if got != test.expected {
				t.Fatalf("shouldRetryComputeBudget() = %t, want %t", got, test.expected)
			}
		})
	}
}
