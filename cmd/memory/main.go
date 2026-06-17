package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/RuntianLee/schema-driven-insight-agent/memory"
	"github.com/RuntianLee/schema-driven-insight-agent/trajectory"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout))
}

func run(args []string, out io.Writer) int {
	flags := flag.NewFlagSet("memory", flag.ContinueOnError)
	flags.SetOutput(out)

	memoryDB := flags.String("memory-db", "memory.db", "memory database path")
	initDB := flags.Bool("init", false, "initialize memory database")
	adapter := flags.String("adapter", "", "adapter name")
	trajectoryDB := flags.String("trajectory-db", "", "trajectory database path")
	manualPath := flags.String("manual", "", "manual notes YAML path")
	search := flags.String("search", "", "search query")
	taskID := flags.String("task", "", "task id filter")
	limit := flags.Int("limit", 5, "search result limit")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if !*initDB && *trajectoryDB == "" && *manualPath == "" && *search == "" {
		fmt.Fprintln(out, "action required: use -init, -trajectory-db, -manual, or -search")
		return 2
	}
	if (*trajectoryDB != "" || *manualPath != "") && *adapter == "" {
		fmt.Fprintln(out, "adapter is required for ingest")
		return 2
	}

	ctx := context.Background()
	db, err := memory.Open(*memoryDB)
	if err != nil {
		fmt.Fprintf(out, "open memory db: %v\n", err)
		return 1
	}
	if err := memory.Migrate(db); err != nil {
		db.Close()
		fmt.Fprintf(out, "migrate memory db: %v\n", err)
		return 1
	}
	store := memory.NewSQLiteStore(db)
	defer store.Close()

	if *initDB {
		fmt.Fprintf(out, "initialized %s\n", *memoryDB)
	}

	if *trajectoryDB != "" {
		trajDB, err := trajectory.Open(*trajectoryDB)
		if err != nil {
			fmt.Fprintf(out, "open trajectory db: %v\n", err)
			return 1
		}
		defer trajDB.Close()
		if err := trajectory.Migrate(trajDB); err != nil {
			fmt.Fprintf(out, "migrate trajectory db: %v\n", err)
			return 1
		}
		report, err := memory.IngestTrajectoryDB(ctx, store, trajDB, memory.IngestOptions{Adapter: *adapter})
		if err != nil {
			fmt.Fprintf(out, "trajectory ingest: %v\n", err)
			return 1
		}
		fmt.Fprintf(out, "trajectory ingest inserted=%d skipped=%d\n", report.Inserted, report.Skipped)
	}

	if *manualPath != "" {
		file, err := os.Open(*manualPath)
		if err != nil {
			fmt.Fprintf(out, "open manual notes: %v\n", err)
			return 1
		}
		defer file.Close()
		report, err := memory.IngestManualNotes(ctx, store, file, memory.ManualOptions{Adapter: *adapter})
		if err != nil {
			fmt.Fprintf(out, "manual ingest: %v\n", err)
			return 1
		}
		fmt.Fprintf(out, "manual ingest inserted=%d skipped=%d\n", report.Inserted, report.Skipped)
	}

	if *search != "" {
		results, err := store.Search(ctx, memory.Query{
			Adapter:  *adapter,
			TaskID:   *taskID,
			Question: *search,
			Limit:    *limit,
		})
		if err != nil {
			fmt.Fprintf(out, "search memory: %v\n", err)
			return 1
		}
		fmt.Fprintln(out, memory.RenderContext(results, memory.ContextOptions{MaxItems: *limit, MaxChars: 1600}))
	}

	return 0
}
