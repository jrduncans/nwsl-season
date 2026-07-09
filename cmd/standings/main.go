package main

import (
	"context"
	"flag"
	"fmt"
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
	cfg := config.FromEnvironment()
	season := flag.String("season", "2026", "NWSL season year to read from cache")
	stage := flag.String("stage", "Regular Season", "NWSL competition stage to read from cache")
	dbPath := flag.String("db", cfg.DBPath, "SQLite cache database path")
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

	table := standings.Calculate(teams, games, standings.DefaultRules())
	printTable(os.Stdout, table)
	return nil
}

func printTable(output *os.File, table []standings.TableRow) {
	writer := tabwriter.NewWriter(output, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "#\tTeam\tP\tW\tD\tL\tGF\tGA\tGD\tPts\tTB")
	for i, row := range table {
		record := row.Record
		fmt.Fprintf(writer, "%d\t%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%s\n",
			i+1,
			displayName(row.Team),
			record.Played,
			record.Wins,
			record.Draws,
			record.Losses,
			record.GoalsFor,
			record.GoalsAgainst,
			record.GoalDifference(),
			record.Points,
			tieBreakNote(row.TieBreak))
	}
	writer.Flush()
}

func displayName(team standings.Team) string {
	switch {
	case team.Name != "":
		return team.Name
	case team.ShortName != "":
		return team.ShortName
	case team.Abbreviation != "":
		return team.Abbreviation
	default:
		return team.ID
	}
}

func tieBreakNote(status standings.TieBreakStatus) string {
	if !status.Undetermined {
		return ""
	}
	return fmt.Sprintf("undetermined at %s", status.Rule)
}
