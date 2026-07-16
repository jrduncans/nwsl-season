package backtest

import (
	"encoding/json"
	"fmt"
	"time"
)

// Report is intentionally small and stable enough for checked-in evidence.
type Report struct {
	Status              string    `json:"status"`
	CurrentDefaultModel string    `json:"current_default_model"`
	GeneratedAt         time.Time `json:"generated_at"`
	Limitations         []string  `json:"limitations"`
}

func JSON(report Report) ([]byte, error) { return json.MarshalIndent(report, "", "  ") }
func Markdown(report Report) string {
	return fmt.Sprintf("# Model evaluation v1\n\nStatus: **%s**. Current default model: **%s**.\n\nGenerated: %s.\n\n## Limitations\n\n- %s\n", report.Status, report.CurrentDefaultModel, report.GeneratedAt.UTC().Format(time.RFC3339), join(report.Limitations, "\n- "))
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
