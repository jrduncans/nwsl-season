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
