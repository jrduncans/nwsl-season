package backtest

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// Report is the stable machine-readable evidence produced by one evaluation.
type Report struct {
	Status              string        `json:"status"`
	IncumbentModel      string        `json:"incumbent_model"`
	SelectedModel       string        `json:"selected_model"`
	GeneratedAt         time.Time     `json:"generated_at"`
	GitCommit           string        `json:"git_commit,omitempty"`
	Iterations          int           `json:"iterations"`
	BootstrapResamples  int           `json:"bootstrap_resamples"`
	ReferenceModels     []string      `json:"reference_models,omitempty"`
	Seasons             []SeasonAudit `json:"seasons"`
	Models              []ModelResult `json:"models"`
	Comparisons         []Comparison  `json:"comparisons"`
	Selection           Selection     `json:"selection"`
	Limitations         []string      `json:"limitations"`
	CurrentDefaultModel string        `json:"current_default_model,omitempty"` // backward-compatible with the not-run artifact
}

type SeasonAudit struct {
	Season          string  `json:"season"`
	Window          string  `json:"window"`
	Included        bool    `json:"included"`
	ExclusionReason string  `json:"exclusion_reason,omitempty"`
	Teams           int     `json:"teams"`
	Games           int     `json:"games"`
	CompletedGames  int     `json:"completed_games"`
	PlayoffPlaces   int     `json:"playoff_places"`
	XGAvailable     int     `json:"xg_available"`
	XGCoverage      float64 `json:"xg_coverage"`
}

type Score struct {
	Mean  float64 `json:"mean"`
	Count int     `json:"count"`
}

type MetricSet struct {
	MatchLogLoss       Score            `json:"match_log_loss"`
	PlayoffBrier       Score            `json:"playoff_brier"`
	ShieldBrier        Score            `json:"shield_brier"`
	PointsMAE          Score            `json:"points_mae"`
	PointsCRPS         Score            `json:"points_crps"`
	PositionMAE        Score            `json:"position_mae"`
	PositionRPS        Score            `json:"position_rps"`
	PlayoffCalibration []CalibrationBin `json:"playoff_calibration"`
	ShieldCalibration  []CalibrationBin `json:"shield_calibration"`
}

type WindowResult struct {
	Metrics MetricSet            `json:"metrics"`
	Stages  map[string]MetricSet `json:"stages"`
}

type ModelResult struct {
	ID      string                  `json:"id"`
	Name    string                  `json:"name"`
	Windows map[string]WindowResult `json:"windows"`
}

type Interval struct {
	PointEstimate float64 `json:"point_estimate"`
	Low           float64 `json:"low"`
	High          float64 `json:"high"`
	Blocks        int     `json:"blocks"`
}

type Comparison struct {
	Candidate string              `json:"candidate"`
	Incumbent string              `json:"incumbent"`
	Metrics   map[string]Interval `json:"metrics"`
}

type Selection struct {
	SelectedModel string            `json:"selected_model"`
	CoverageGate  bool              `json:"coverage_gate"`
	Candidates    []CandidateResult `json:"candidates"`
	Reason        string            `json:"reason"`
}

type CandidateResult struct {
	Model     string   `json:"model"`
	Qualified bool     `json:"qualified"`
	Reasons   []string `json:"reasons"`
}

func JSON(report Report) ([]byte, error) {
	value, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(value, '\n'), nil
}

func Markdown(report Report) string {
	var out strings.Builder
	selected := report.SelectedModel
	if selected == "" {
		selected = report.CurrentDefaultModel
	}
	fmt.Fprintln(&out, "# Model evaluation v1")
	fmt.Fprintln(&out)
	fmt.Fprintf(&out, "Status: **%s**. Selected model: **%s**.\n\n", report.Status, selected)
	fmt.Fprintf(&out, "Generated: %s. Simulations: %s iterations per cutoff; %s paired bootstrap resamples.\n\n", report.GeneratedAt.UTC().Format(time.RFC3339), comma(report.Iterations), comma(report.BootstrapResamples))
	if report.GitCommit != "" {
		fmt.Fprintf(&out, "Git commit: `%s`.\n\n", report.GitCommit)
	}
	fmt.Fprintln(&out, "## Data audit")
	fmt.Fprintln(&out)
	fmt.Fprintln(&out, "| Season | Window | Included | Completed | xG coverage | Note |")
	fmt.Fprintln(&out, "| --- | --- | ---: | ---: | ---: | --- |")
	for _, season := range report.Seasons {
		note := season.ExclusionReason
		if note == "" {
			note = "—"
		}
		fmt.Fprintf(&out, "| %s | %s | %t | %d | %.1f%% | %s |\n", season.Season, season.Window, season.Included, season.CompletedGames, 100*season.XGCoverage, escapePipe(note))
	}
	fmt.Fprintln(&out)
	fmt.Fprintln(&out, "## Evaluation protocol")
	fmt.Fprintln(&out)
	fmt.Fprintf(&out, "Development: %s. These seasons may guide new candidate model versions and their fixed constants.\n\n", windowSeasons(report, DevelopmentWindow))
	fmt.Fprintf(&out, "Final test: %s. These seasons are held out from model design and alone determine the recommendation.\n\n", windowSeasons(report, HeldoutWindow))
	fmt.Fprintln(&out, "Pooled results combine both windows for descriptive context only; they never determine the recommendation.")
	fmt.Fprintln(&out)
	fmt.Fprintln(&out, "A formula, prior, or weight changed after inspecting the final-test results is a new model version and must wait for new untouched seasons before it can claim a final-test result.")
	fmt.Fprintln(&out)
	fmt.Fprintln(&out, "## Summary results")
	fmt.Fprintln(&out)
	fmt.Fprintln(&out, "Lower is better for every metric.")
	fmt.Fprintln(&out)
	writeSummaryTable(&out, "Development results", report.Models, DevelopmentWindow)
	writeSummaryTable(&out, "Final-test results", report.Models, HeldoutWindow)
	writeSummaryTable(&out, "Pooled results (descriptive only)", report.Models, "all")
	fmt.Fprintln(&out, "## Paired final-test comparisons")
	fmt.Fprintln(&out)
	fmt.Fprintln(&out, "Differences are candidate minus incumbent; negative values favor the candidate.")
	fmt.Fprintln(&out)
	fmt.Fprintln(&out, "| Candidate | Metric | Difference | 95% interval | Date blocks |")
	fmt.Fprintln(&out, "| --- | --- | ---: | ---: | ---: |")
	comparisonMetrics := []string{"match_log_loss", "playoff_brier", "shield_brier", "points_crps", "position_rps"}
	for _, comparison := range report.Comparisons {
		for _, name := range comparisonMetrics {
			interval := comparison.Metrics[name]
			fmt.Fprintf(&out, "| `%s` | %s | %+.4f | [%+.4f, %+.4f] | %d |\n", comparison.Candidate, name, interval.PointEstimate, interval.Low, interval.High, interval.Blocks)
		}
	}
	fmt.Fprintln(&out)
	fmt.Fprintln(&out, "The JSON artifact is the machine-readable source for all development/final-test stage buckets and fixed-decile calibration tables.")
	fmt.Fprintln(&out)
	fmt.Fprintln(&out, "## Selection")
	fmt.Fprintln(&out)
	fmt.Fprintf(&out, "%s\n\n", report.Selection.Reason)
	for _, candidate := range report.Selection.Candidates {
		label := "did not qualify"
		if candidate.Qualified {
			label = "qualified"
		}
		fmt.Fprintf(&out, "- `%s` %s: %s.\n", candidate.Model, label, strings.Join(candidate.Reasons, "; "))
	}
	fmt.Fprintln(&out)
	fmt.Fprintln(&out, "## Limitations")
	fmt.Fprintln(&out)
	if len(report.Limitations) == 0 {
		fmt.Fprintln(&out, "- No evaluation limitations recorded.")
	}
	for _, limitation := range report.Limitations {
		fmt.Fprintf(&out, "- %s\n", limitation)
	}
	return out.String()
}

func writeSummaryTable(out *strings.Builder, title string, models []ModelResult, window string) {
	fmt.Fprintf(out, "### %s\n\n", title)
	fmt.Fprintln(out, "| Model | Match log loss | Playoff Brier | Shield Brier | Points MAE | Points CRPS | Position MAE | Position RPS |")
	fmt.Fprintln(out, "| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |")
	for _, model := range models {
		metrics := model.Windows[window].Metrics
		fmt.Fprintf(out, "| %s (`%s`) | %.4f | %.4f | %.4f | %.3f | %.3f | %.3f | %.4f |\n", model.Name, model.ID, metrics.MatchLogLoss.Mean, metrics.PlayoffBrier.Mean, metrics.ShieldBrier.Mean, metrics.PointsMAE.Mean, metrics.PointsCRPS.Mean, metrics.PositionMAE.Mean, metrics.PositionRPS.Mean)
	}
	fmt.Fprintln(out)
}

func windowSeasons(report Report, window string) string {
	values := []string{}
	for _, season := range report.Seasons {
		if season.Window == window {
			values = append(values, season.Season)
		}
	}
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ", ")
}

func comparisons(cfg Config, models map[string]*modelAccumulator) []Comparison {
	incumbent := models[cfg.IncumbentModelID]
	metricNames := []string{"match_log_loss", "playoff_brier", "shield_brier", "points_crps", "position_rps"}
	results := []Comparison{}
	for _, model := range cfg.Models {
		candidateID := model.Info().ID
		if candidateID == cfg.IncumbentModelID {
			continue
		}
		candidate := models[candidateID]
		keys := commonKeys(candidate.blocks[HeldoutWindow], incumbent.blocks[HeldoutWindow])
		comparison := Comparison{Candidate: candidateID, Incumbent: cfg.IncumbentModelID, Metrics: map[string]Interval{}}
		for metricIndex, name := range metricNames {
			differences := make([]float64, 0, len(keys))
			for _, key := range keys {
				differences = append(differences, metricValue(candidate.blocks[HeldoutWindow][key], metricIndex)-metricValue(incumbent.blocks[HeldoutWindow][key], metricIndex))
			}
			low, high := PairedBootstrap(differences, cfg.BootstrapResamples, cfg.BootstrapSeed+int64(metricIndex))
			comparison.Metrics[name] = Interval{PointEstimate: mean(differences), Low: low, High: high, Blocks: len(differences)}
		}
		results = append(results, comparison)
	}
	return results
}

func selectModel(report Report, coverageGate bool) Selection {
	selection := Selection{SelectedModel: report.IncumbentModel, CoverageGate: coverageGate}
	bestID, bestLogLoss := "", math.Inf(1)
	for _, comparison := range report.Comparisons {
		if isReferenceModel(report, comparison.Candidate) {
			selection.Candidates = append(selection.Candidates, CandidateResult{Model: comparison.Candidate, Reasons: []string{"evaluation-only reference model; excluded from selection"}})
			continue
		}
		reasons := []string{}
		logLoss := comparison.Metrics["match_log_loss"]
		metrics := modelMetrics(report, comparison.Candidate)
		incumbent := modelMetrics(report, report.IncumbentModel)
		if !coverageGate {
			reasons = append(reasons, "final-test audit/xG coverage gate failed")
		}
		if logLoss.Blocks == 0 || logLoss.High >= 0 {
			reasons = append(reasons, "final-test log-loss bootstrap interval was not entirely below zero")
		}
		if metrics.PlayoffBrier.Mean-incumbent.PlayoffBrier.Mean > .005 {
			reasons = append(reasons, "playoff Brier guardrail failed")
		}
		if metrics.ShieldBrier.Mean-incumbent.ShieldBrier.Mean > .002 {
			reasons = append(reasons, "Shield Brier guardrail failed")
		}
		if metrics.PointsCRPS.Mean-incumbent.PointsCRPS.Mean > .25 {
			reasons = append(reasons, "points CRPS guardrail failed")
		}
		if metrics.PositionRPS.Mean-incumbent.PositionRPS.Mean > .02 {
			reasons = append(reasons, "position RPS guardrail failed")
		}
		qualified := len(reasons) == 0
		if qualified {
			reasons = append(reasons, "passed the precommitted bootstrap and guardrail rule")
			if metrics.MatchLogLoss.Mean < bestLogLoss {
				bestID, bestLogLoss = comparison.Candidate, metrics.MatchLogLoss.Mean
			}
		}
		selection.Candidates = append(selection.Candidates, CandidateResult{Model: comparison.Candidate, Qualified: qualified, Reasons: reasons})
	}
	if bestID == "" {
		bestID = report.IncumbentModel
	}
	selection.SelectedModel = bestID
	if bestID == report.IncumbentModel {
		selection.Reason = fmt.Sprintf("No candidate met the precommitted replacement rule, so `%s` remains selected.", bestID)
	} else {
		selection.Reason = fmt.Sprintf("`%s` met the precommitted replacement rule and had the lowest qualifying final-test match log loss.", bestID)
	}
	return selection
}

func isReferenceModel(report Report, id string) bool {
	for _, reference := range report.ReferenceModels {
		if id == reference {
			return true
		}
	}
	return false
}

func modelMetrics(report Report, id string) MetricSet {
	for _, model := range report.Models {
		if model.ID == id {
			return model.Windows[HeldoutWindow].Metrics
		}
	}
	return MetricSet{}
}

func metricValue(metrics MetricSet, index int) float64 {
	return []float64{metrics.MatchLogLoss.Mean, metrics.PlayoffBrier.Mean, metrics.ShieldBrier.Mean, metrics.PointsCRPS.Mean, metrics.PositionRPS.Mean}[index]
}

func commonKeys(left, right map[string]MetricSet) []string {
	keys := []string{}
	for key := range left {
		if _, ok := right[key]; ok {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func mean(values []float64) float64 {
	total := 0.0
	for _, value := range values {
		total += value
	}
	return divide(total, len(values))
}

func comma(value int) string {
	text := fmt.Sprintf("%d", value)
	for i := len(text) - 3; i > 0; i -= 3 {
		text = text[:i] + "," + text[i:]
	}
	return text
}

func escapePipe(value string) string { return strings.ReplaceAll(value, "|", "\\|") }
