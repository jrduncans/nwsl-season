package scenarios

import (
	"reflect"
	"testing"
)

func TestJoinHeadToHeadPreservesValidScoreConditions(t *testing.T) {
	headToHead := ScoreCondition{TeamID: "A", RivalID: "B", Levels: []ScoreLevel{
		{Measure: MeasureHeadToHeadGoals, MinimumGain: 1, TeamNow: 0, RivalNow: 0},
	}}
	prefix := ScoreCondition{TeamID: "A", RivalID: "B", Levels: []ScoreLevel{
		{Measure: MeasureGoalDifference, MinimumGain: 1, ExactOnly: true},
		{Measure: MeasureGoalsScored, MinimumGain: 1, ExactOnly: true},
	}}
	merged := ScoreCondition{TeamID: "A", RivalID: "B", Levels: []ScoreLevel{
		prefix.Levels[0], prefix.Levels[1], headToHead.Levels[0],
	}}
	for _, test := range []struct {
		name string
		in   []ScoreCondition
		want []ScoreCondition
	}{
		{"standalone", []ScoreCondition{headToHead}, []ScoreCondition{headToHead}},
		{"after level condition", []ScoreCondition{prefix, headToHead}, []ScoreCondition{merged}},
		{"before level condition", []ScoreCondition{headToHead, prefix}, []ScoreCondition{merged}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := joinHeadToHead(test.in)
			if err := validateScoreConditions(got, "A"); err != nil {
				t.Fatalf("joined conditions are invalid: %v; got %+v", err, got)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("joined conditions = %+v, want %+v", got, test.want)
			}
		})
	}
}
