package syncer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jrduncans/nwsl-season/internal/asa"
	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/telemetry"
	"github.com/jrduncans/nwsl-season/internal/telemetry/nwslconv"
	"go.opentelemetry.io/otel/trace"
)

// penaltyXGASAClient is the optional penalty-pattern xG source. Penalty data
// rides inside the game xG operation, so a client without it simply yields no
// splits.
type penaltyXGASAClient interface {
	GamePenaltyXGoals(context.Context, asa.PenaltyXGoalsFilters) ([]asa.TeamGamePenaltyXGoals, error)
}

// penaltyTolerance covers ASA's 4-decimal rounding of xG values.
const penaltyTolerance = 0.0001 + 1e-9

// penaltyOutcome summarizes one operation's penalty observation.
type penaltyOutcome struct {
	Requested bool
	Accepted  int
	Rejected  int
}

// attachPenaltySplits makes the one penalty request for an xG operation and
// sets Penalties on each accepted game in mapped (index-aligned with values).
// Every failure is absorbed: games without an accepted split keep a nil split,
// which the cache treats as "not observed".
func (s Service) attachPenaltySplits(ctx context.Context, operation Operation, values []asa.GameXGoals, mapped []cache.GameXG) penaltyOutcome {
	client, ok := s.ASA.(penaltyXGASAClient)
	if !ok || len(values) == 0 {
		return penaltyOutcome{}
	}
	outcome := penaltyOutcome{Requested: true}
	// The failure is absorbed and the operation still succeeds, so the exception
	// log is correlated through ctx but must not mark the operation span failed.
	absorbed := trace.SpanFromContext(context.Background())
	filters, err := penaltyFilters(operation, values)
	var rows []asa.TeamGamePenaltyXGoals
	if err == nil {
		rows, err = client.GamePenaltyXGoals(ctx, filters)
	}
	if err != nil {
		_ = telemetry.RecordWarningWithType(ctx, absorbed, fmt.Errorf("fetch penalty xG: %w", err), nwslconv.ErrorCodeSyncPenaltyXG, telemetry.ErrorTypeUpstreamFailure)
		return outcome
	}

	byGame := make(map[string][]asa.TeamGamePenaltyXGoals, len(values))
	for _, row := range rows {
		byGame[row.GameID] = append(byGame[row.GameID], row)
	}
	var firstRejection error
	for index, value := range values {
		split, raw, err := validatePenaltyGame(value, byGame[value.GameID])
		if err != nil {
			outcome.Rejected++
			if firstRejection == nil {
				firstRejection = err
			}
			continue
		}
		mapped[index].Penalties = split
		mapped[index].PenaltyRawJSON = raw
		outcome.Accepted++
	}
	if firstRejection != nil {
		_ = telemetry.RecordWarningWithType(ctx, absorbed, fmt.Errorf("validate penalty xG: %d game(s) rejected, first: %w", outcome.Rejected, firstRejection), nwslconv.ErrorCodeSyncPenaltyXG, telemetry.ErrorTypeInvalidData)
	}
	return outcome
}

// penaltyFilters selects the season for full operations. Targeted operations
// use a date window around the returned games, padded by one day on each side
// because ASA's date-filter time zone is unverified. It never filters by game.
func penaltyFilters(operation Operation, values []asa.GameXGoals) (asa.PenaltyXGoalsFilters, error) {
	if operation.Mode != OperationTargeted {
		return asa.PenaltyXGoalsFilters{SeasonName: operation.Season, StageName: operation.Stage}, nil
	}
	var first, last time.Time
	for _, value := range values {
		at, err := parseASATimestamp(value.DateTimeUTC)
		if err != nil {
			return asa.PenaltyXGoalsFilters{}, fmt.Errorf("penalty window for game %q: %w", value.GameID, err)
		}
		if first.IsZero() || at.Before(first) {
			first = at
		}
		if last.IsZero() || at.After(last) {
			last = at
		}
	}
	return asa.PenaltyXGoalsFilters{
		StageName: operation.Stage,
		StartDate: first.AddDate(0, 0, -1).Format("2006-01-02"),
		EndDate:   last.AddDate(0, 0, 1).Format("2006-01-02"),
	}, nil
}

func parseASATimestamp(value string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02 15:04:05 MST", time.RFC3339} {
		if at, err := time.Parse(layout, value); err == nil {
			return at.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unparseable date_time_utc %q", value)
}

// validatePenaltyGame accepts a game's split only when its two team rows are
// consistent with each other and with the game's own xG.
func validatePenaltyGame(game asa.GameXGoals, rows []asa.TeamGamePenaltyXGoals) (*cache.PenaltySplit, string, error) {
	if len(rows) != 2 {
		return nil, "", fmt.Errorf("game %q has %d penalty rows, want 2", game.GameID, len(rows))
	}
	var home, away *asa.TeamGamePenaltyXGoals
	for index := range rows {
		switch rows[index].TeamID {
		case game.HomeTeamID:
			home = &rows[index]
		case game.AwayTeamID:
			away = &rows[index]
		}
	}
	if home == nil || away == nil {
		return nil, "", fmt.Errorf("game %q penalty rows do not match its teams", game.GameID)
	}
	for _, row := range []*asa.TeamGamePenaltyXGoals{home, away} {
		if !finiteNonnegative(row.XGoalsFor) || !finiteNonnegative(row.XGoalsAgainst) ||
			row.ShotsFor < 0 || row.ShotsAgainst < 0 || row.GoalsFor < 0 || row.GoalsAgainst < 0 {
			return nil, "", fmt.Errorf("game %q has invalid penalty values", game.GameID)
		}
	}
	if math.Abs(home.XGoalsFor-away.XGoalsAgainst) > penaltyTolerance || math.Abs(away.XGoalsFor-home.XGoalsAgainst) > penaltyTolerance ||
		home.GoalsFor != away.GoalsAgainst || away.GoalsFor != home.GoalsAgainst ||
		home.ShotsFor != away.ShotsAgainst || away.ShotsFor != home.ShotsAgainst {
		return nil, "", fmt.Errorf("game %q penalty rows disagree", game.GameID)
	}
	if home.XGoalsFor > game.HomeTeamXGoals+penaltyTolerance || away.XGoalsFor > game.AwayTeamXGoals+penaltyTolerance {
		return nil, "", fmt.Errorf("game %q penalty xG exceeds game xG", game.GameID)
	}
	raw, err := penaltyRawJSON(home, away)
	if err != nil {
		return nil, "", err
	}
	return &cache.PenaltySplit{
		HomeXG: home.XGoalsFor, AwayXG: away.XGoalsFor,
		HomeGoals: home.GoalsFor, AwayGoals: away.GoalsFor,
		HomeShots: home.ShotsFor, AwayShots: away.ShotsFor,
	}, raw, nil
}

func penaltyRawJSON(home, away *asa.TeamGamePenaltyXGoals) (string, error) {
	parts := make([]json.RawMessage, 0, 2)
	for _, row := range []*asa.TeamGamePenaltyXGoals{home, away} {
		raw := row.RawJSON
		if raw == "" {
			encoded, err := json.Marshal(row)
			if err != nil {
				return "", err
			}
			raw = string(encoded)
		}
		if !json.Valid([]byte(raw)) {
			return "", errors.New("penalty source object is not valid JSON")
		}
		parts = append(parts, json.RawMessage(raw))
	}
	encoded, err := json.Marshal(parts)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}
