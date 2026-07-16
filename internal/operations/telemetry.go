// Package operations contains shared command-level operational helpers.
package operations

import (
	"log/slog"

	"github.com/jrduncans/nwsl-season/internal/qualification"
	"github.com/jrduncans/nwsl-season/internal/scenariorefresh"
)

func QualificationTelemetry(logger *slog.Logger) func(qualification.Progress) {
	return func(value qualification.Progress) {
		logger.Info("qualification proof "+value.Phase,
			"team_id", value.TeamID, "achievement", value.Achievement.ID, "top_k", value.Achievement.TopK,
			"completed", value.Completed, "total", value.Total, "elapsed", value.Elapsed,
			"batch_elapsed", value.BatchElapsed, "status", value.Status, "method", value.Method, "no_help_state", value.NoHelpState)
	}
}

func ScenarioTelemetry(logger *slog.Logger) func(scenariorefresh.Progress) {
	return func(value scenariorefresh.Progress) {
		logger.Info("clinching scenario "+value.Phase,
			"team_id", value.TeamID, "achievement", value.Achievement.ID, "top_k", value.Achievement.TopK,
			"completed", value.Completed, "total", value.Total, "elapsed", value.Elapsed,
			"batch_elapsed", value.BatchElapsed, "state", value.State)
	}
}
