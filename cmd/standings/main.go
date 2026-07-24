package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/config"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "standings: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	if err := config.LoadEnvironmentFile(); err != nil {
		return fmt.Errorf("load configuration environment file: %w", err)
	}
	cfg, err := config.FromEnvironment()
	if err != nil {
		return fmt.Errorf("configuration: %w", err)
	}
	season := flag.String("season", "2026", "NWSL season year to read from cache")
	stage := flag.String("stage", "Regular Season", "NWSL competition stage to read from cache")
	dbPath := flag.String("db", cfg.DBPath, "SQLite cache database path")
	order := flag.String("order", "per-game", "standings order: per-game or total")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	db, err := cache.Open(ctx, *dbPath)
	if err != nil {
		return fmt.Errorf("open cache database %q: %w", *dbPath, err)
	}
	defer db.Close()

	teams, games, err := db.StandingsInputs(ctx, *season, *stage)
	if err != nil {
		return fmt.Errorf("load %s %s standings inputs: %w", *season, *stage, err)
	}

	rules, err := standingsRules(*order)
	if err != nil {
		return err
	}

	table := standings.Calculate(teams, games, rules)
	printTable(os.Stdout, table)
	return nil
}

func standingsRules(order string) (standings.Rules, error) {
	switch order {
	case "per-game":
		return standings.PerGameRules(), nil
	case "total":
		return standings.OfficialTotalRules(), nil
	default:
		return standings.Rules{}, fmt.Errorf("unknown standings order %q: use per-game or total", order)
	}
}

func printTable(output io.Writer, table []standings.TableRow) {
	writer := tabwriter.NewWriter(output, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "#\tTeam\tP\tW\tD\tL\tGF\tGA\tGD\tPts\tPPG\tTB")
	for i, row := range table {
		record := row.Record
		fmt.Fprintf(writer, "%d\t%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%.2f\t%s\n",
			i+1,
			standings.DisplayName(row.Team),
			record.Played,
			record.Wins,
			record.Draws,
			record.Losses,
			record.GoalsFor,
			record.GoalsAgainst,
			record.GoalDifference(),
			record.Points,
			pointsPerGame(record),
			tieBreakNote(row.TieBreak))
	}
	writer.Flush()
}

func pointsPerGame(record standings.Record) float64 {
	if record.Played == 0 {
		return 0
	}
	return float64(record.Points) / float64(record.Played)
}

func tieBreakNote(status standings.TieBreakStatus) string {
	if !status.Undetermined {
		return ""
	}
	return fmt.Sprintf("undetermined at %s", status.Rule)
}
