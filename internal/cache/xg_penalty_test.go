package cache

import (
	"strings"
	"testing"
	"time"
)

func penaltySplitValue() *PenaltySplit {
	return &PenaltySplit{HomeXG: .76, HomeGoals: 1, HomeShots: 1}
}

func penaltyXGValue(id string, split *PenaltySplit) GameXG {
	value := xgValue(id)
	value.RawJSON = `{"v":1}`
	value.Penalties = split
	if split != nil {
		value.PenaltyRawJSON = `[{"home":true},{"away":true}]`
	}
	return value
}

func TestMigrationFifteenKeepsExistingXGWithNullPenalties(t *testing.T) {
	t.Parallel()
	ctx, db, _, path := checkedXGFixture(t)
	if _, err := db.ReplaceStageXG(ctx, "2031", "Checks", []GameXG{penaltyXGValue("one", nil)}, xgMetadataAt(time.Date(2031, 1, 1, 0, 1, 0, 0, time.UTC))); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`ALTER TABLE game_xg DROP COLUMN home_penalty_xg`, `ALTER TABLE game_xg DROP COLUMN away_penalty_xg`,
		`ALTER TABLE game_xg DROP COLUMN home_penalty_goals`, `ALTER TABLE game_xg DROP COLUMN away_penalty_goals`,
		`ALTER TABLE game_xg DROP COLUMN home_penalty_shots`, `ALTER TABLE game_xg DROP COLUMN away_penalty_shots`,
		`ALTER TABLE game_xg DROP COLUMN penalty_raw_json`, `DELETE FROM schema_migrations WHERE version=17`, `DELETE FROM schema_migrations WHERE version=16`, `DELETE FROM schema_migrations WHERE version=15`,
	} {
		if _, err := db.db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int
	if err := db.db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil || version != 17 {
		t.Fatalf("version=%d,%v", version, err)
	}
	state, ok, err := db.GameXGState(ctx, "one")
	if err != nil || !ok || state.Availability != XGAvailable || state.HomeXG.Float64 != 1.2 || state.AwayXG.Float64 != .8 || state.RawJSON != `{"v":1}` || state.Penalties != nil || state.PenaltyRawJSON != "" {
		t.Fatalf("migrated state=%+v ok=%t err=%v", state, ok, err)
	}
}

func TestPenaltySplitWriteSemantics(t *testing.T) {
	t.Parallel()
	other := &PenaltySplit{HomeXG: .5, AwayXG: .1, HomeGoals: 1, HomeShots: 2, AwayShots: 1}
	tests := []struct {
		name          string
		stored        *PenaltySplit
		incoming      *PenaltySplit
		want          *PenaltySplit
		wantUpdated   int
		wantUnchanged int
	}{
		{name: "first split", incoming: penaltySplitValue(), want: penaltySplitValue(), wantUpdated: 1},
		{name: "nil preserves stored split", stored: penaltySplitValue(), incoming: nil, want: penaltySplitValue(), wantUnchanged: 1},
		{name: "same split is unchanged", stored: penaltySplitValue(), incoming: penaltySplitValue(), want: penaltySplitValue(), wantUnchanged: 1},
		{name: "different split overwrites", stored: penaltySplitValue(), incoming: other, want: other, wantUpdated: 1},
		{name: "nil stays nil", wantUnchanged: 1},
	}
	for _, mode := range []string{"full", "targeted"} {
		for _, test := range tests {
			t.Run(mode+"/"+test.name, func(t *testing.T) {
				t.Parallel()
				ctx, db, _, _ := checkedXGFixture(t)
				defer db.Close()
				first := time.Date(2031, 1, 1, 1, 0, 0, 0, time.UTC)
				if _, err := db.ReplaceStageXG(ctx, "2031", "Checks", []GameXG{penaltyXGValue("one", test.stored)}, xgMetadataAt(first)); err != nil {
					t.Fatal(err)
				}
				var updated, inserted, unchanged int
				var downstream bool
				incoming := penaltyXGValue("one", test.incoming)
				if mode == "full" {
					result, err := db.ReplaceStageXG(ctx, "2031", "Checks", []GameXG{incoming}, xgMetadataAt(first.Add(time.Hour)))
					if err != nil {
						t.Fatal(err)
					}
					updated, inserted, unchanged, downstream = result.Audit.RowsUpdated, result.Audit.RowsInserted, result.Audit.RowsUnchanged, result.Audit.DownstreamInputsChanged
				} else {
					metadata := TargetedRefreshMetadata{Trigger: SourceTriggerScheduler, StartedAt: first.Add(time.Hour), FinishedAt: first.Add(time.Hour + time.Minute)}
					due := metadata.FinishedAt.Add(time.Hour)
					result, err := db.UpsertCheckedXG(ctx, "2031", "Checks", []CheckedXGRequest{{GameID: "one", NextDueAt: &due}}, []GameXG{incoming}, metadata)
					if err != nil {
						t.Fatal(err)
					}
					updated, inserted, unchanged, downstream = result.Audit.RowsUpdated, result.Audit.RowsInserted, result.Audit.RowsUnchanged, result.Audit.DownstreamInputsChanged
				}
				wantUnchanged := test.wantUnchanged
				if mode == "full" {
					wantUnchanged++ // the second fixture game stays an unavailable marker
				}
				if inserted != 0 || updated != test.wantUpdated || unchanged != wantUnchanged {
					t.Fatalf("inserted/updated/unchanged = %d/%d/%d, want 0/%d/%d", inserted, updated, unchanged, test.wantUpdated, wantUnchanged)
				}
				if downstream {
					t.Fatal("penalty-only change must not be a material downstream change")
				}
				state, ok, err := db.GameXGState(ctx, "one")
				if err != nil || !ok {
					t.Fatalf("state ok=%t err=%v", ok, err)
				}
				if (state.Penalties == nil) != (test.want == nil) || (test.want != nil && *state.Penalties != *test.want) {
					t.Fatalf("stored split = %+v, want %+v", state.Penalties, test.want)
				}
				if test.want != nil && state.PenaltyRawJSON == "" {
					t.Fatal("stored split lost its raw JSON")
				}
				states, err := db.GameXGStates(ctx, "2031", "Checks")
				if err != nil || len(states) == 0 {
					t.Fatalf("states=%+v,%v", states, err)
				}
				data, err := db.Season(ctx, "2031", "Checks")
				if err != nil {
					t.Fatal(err)
				}
				for _, value := range data.XGoals {
					if value.GameID == "one" && (value.Penalties == nil) != (test.want == nil) {
						t.Fatalf("Season split = %+v, want %+v", value.Penalties, test.want)
					}
				}
			})
		}
	}
}

func TestPenaltySplitRejectsInvalidValues(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		value GameXG
	}{
		{name: "negative xG", value: penaltyXGValue("one", &PenaltySplit{HomeXG: -1})},
		{name: "negative goals", value: penaltyXGValue("one", &PenaltySplit{HomeGoals: -1})},
		{name: "raw json without split", value: func() GameXG { v := xgValue("one"); v.PenaltyRawJSON = "x"; return v }()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx, db, _, _ := checkedXGFixture(t)
			defer db.Close()
			if _, err := db.ReplaceStageXG(ctx, "2031", "Checks", []GameXG{test.value}, xgMetadataAt(time.Date(2031, 1, 1, 1, 0, 0, 0, time.UTC))); err == nil {
				t.Fatal("ReplaceStageXG accepted invalid penalty split")
			}
			due := time.Date(2031, 1, 3, 0, 0, 0, 0, time.UTC)
			if _, err := db.UpsertCheckedXG(ctx, "2031", "Checks", []CheckedXGRequest{{GameID: "one", NextDueAt: &due}}, []GameXG{test.value}, xgCheckMetadata(1)); err == nil {
				t.Fatal("UpsertCheckedXG accepted invalid penalty split")
			}
		})
	}
}

func TestUnavailableRowsRejectPenaltySplits(t *testing.T) {
	t.Parallel()
	ctx, db, _, _ := checkedXGFixture(t)
	defer db.Close()
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	value := GameXG{GameID: "two", Availability: XGUnavailable, HomeTeamID: "alpha", AwayTeamID: "bravo", Penalties: penaltySplitValue(), PenaltyRawJSON: "[]"}
	if _, err := writeGameXG(ctx, tx, value, time.Date(2031, 1, 1, 0, 0, 0, 0, time.UTC)); err == nil || !strings.Contains(err.Error(), "penalty") {
		t.Fatalf("unavailable split err = %v, want penalty rejection", err)
	}
}

func TestStoredPenaltyColumnsAreAllOrNone(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		statement string
	}{
		{name: "partial columns", statement: `UPDATE game_xg SET away_penalty_xg=NULL WHERE asa_game_id='one'`},
		{name: "negative value", statement: `UPDATE game_xg SET home_penalty_xg=-0.5 WHERE asa_game_id='one'`},
		{name: "raw without split", statement: `UPDATE game_xg SET home_penalty_xg=NULL,away_penalty_xg=NULL,home_penalty_goals=NULL,away_penalty_goals=NULL,home_penalty_shots=NULL,away_penalty_shots=NULL WHERE asa_game_id='one'`},
		{name: "split on unavailable row", statement: `UPDATE game_xg SET availability='unavailable',home_xg=NULL,away_xg=NULL,home_xpoints=NULL,away_xpoints=NULL,first_observed_at=NULL,raw_json='' WHERE asa_game_id='one'`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx, db, _, _ := checkedXGFixture(t)
			defer db.Close()
			if _, err := db.ReplaceStageXG(ctx, "2031", "Checks", []GameXG{penaltyXGValue("one", penaltySplitValue())}, xgMetadataAt(time.Date(2031, 1, 1, 1, 0, 0, 0, time.UTC))); err != nil {
				t.Fatal(err)
			}
			if _, err := db.db.ExecContext(ctx, test.statement); err != nil {
				t.Fatal(err)
			}
			if _, _, err := db.GameXGState(ctx, "one"); err == nil {
				t.Fatal("GameXGState accepted invalid stored penalty columns")
			}
			if _, err := db.GameXGStates(ctx, "2031", "Checks"); err == nil {
				t.Fatal("GameXGStates accepted invalid stored penalty columns")
			}
		})
	}
}
