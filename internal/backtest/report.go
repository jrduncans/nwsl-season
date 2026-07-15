package backtest

import (
	"encoding/json"
	"fmt"
	"time"
)

// Report is intentionally small and stable enough for checked-in evidence.
type Report struct {
	EvidenceID       string    `json:"evidence_id"`
	RecommendedModel string    `json:"recommended_model"`
	GeneratedAt      time.Time `json:"generated_at"`
	Iterations       int       `json:"iterations"`
	Limitations      []string  `json:"limitations"`
}

func JSON(report Report) ([]byte, error) { return json.MarshalIndent(report, "", "  ") }
func Markdown(report Report) string {
	return fmt.Sprintf("# Model evaluation v1\n\nRecommended model: **%s**. Evidence ID: `%s`.\n\nGenerated: %s. Back-test iterations: %d.\n\n## Limitations\n\n- %s\n", report.RecommendedModel, report.EvidenceID, report.GeneratedAt.UTC().Format(time.RFC3339), report.Iterations, join(report.Limitations, "\n- "))
}
func join(values []string, separator string) string {
	if len(values) == 0 {
		return "No evaluation limitations recorded."
	}
	out := values[0]
	for _, value := range values[1:] {
		out += separator + value
	}
	return out
}
