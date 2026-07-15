package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/jrduncans/nwsl-season/internal/backtest"
	"github.com/jrduncans/nwsl-season/internal/forecast"
)

// The evaluation command intentionally writes only explicit artifacts. Cache
// acquisition/backfill remains an operator step, so ordinary tests never need
// the network or a live SQLite file.
func main() {
	output := flag.String("output-dir", "docs", "directory for model-evaluation-v1 artifacts")
	generated := flag.String("generated-at", "", "RFC3339 generation time (for reproducible reports)")
	iterations := flag.Int("iterations", 20000, "simulation iterations per daily cutoff")
	flag.Parse()
	at := time.Now().UTC()
	if *generated != "" {
		parsed, err := time.Parse(time.RFC3339, *generated)
		if err != nil {
			fmt.Fprintln(os.Stderr, "backtest:", err)
			os.Exit(2)
		}
		at = parsed
	}
	report := backtest.Report{EvidenceID: "model-evaluation-v1", RecommendedModel: forecast.Recommended().Model.Info().ID, GeneratedAt: at, Iterations: *iterations, Limitations: []string{"This command records a deterministic evidence envelope; run it only after the local historical cache has been audited.", "Historical xG availability reflects currently published ASA values, not original publication timing."}}
	jsonData, err := backtest.JSON(report)
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(*output+"/model-evaluation-v1.json", jsonData, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "backtest:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*output+"/model-evaluation-v1.md", []byte(backtest.Markdown(report)), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "backtest:", err)
		os.Exit(1)
	}
}
