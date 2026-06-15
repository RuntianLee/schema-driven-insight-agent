package memory

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/RuntianLee/schema-driven-insight-agent/trajectory"
)

func TestIngestTrajectoryDBOnlyKeepsSuccessfulEvaluatedRuns(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	trajDB := newTrajectoryDB(t)

	insertTrajectory(t, trajDB, "traj-ok", "benchmark", "success",
		"How should whale retention be analyzed?",
		"Use analyze for whale retention cohorts.")
	insertStep(t, trajDB, "traj-ok", 0, "tool_call", "analyze",
		`{"table":"player_basics"}`, `{"status":"OK"}`)
	insertEval(t, trajDB, "traj-ok", "big_r_retention", "data_correctness", 1, 1.0)

	insertTrajectory(t, trajDB, "traj-fail", "benchmark", "error",
		"broken whale retention question", "broken output")
	insertStep(t, trajDB, "traj-fail", 0, "tool_call", "analyze",
		`{"table":"player_basics"}`, `{"status":"ERR"}`)
	insertEval(t, trajDB, "traj-fail", "broken_task", "data_correctness", 0, 0.0)

	insertTrajectory(t, trajDB, "traj-no-eval", "benchmark", "success",
		"successful run without eval", "should not enter memory")
	insertStep(t, trajDB, "traj-no-eval", 0, "tool_call", "analyze",
		`{"table":"player_basics"}`, `{"status":"OK"}`)

	report, err := IngestTrajectoryDB(ctx, store, trajDB, IngestOptions{Adapter: "b3"})
	if err != nil {
		t.Fatalf("ingest trajectory db: %v", err)
	}
	if report.Inserted != 1 || report.Skipped != 2 {
		t.Fatalf("report=%+v want inserted=1 skipped=2", report)
	}

	results, err := store.Search(ctx, Query{
		Adapter:  "b3",
		TaskID:   "big_r_retention",
		Question: "whale retention analyze",
		Limit:    5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("results len=%d want 1: %#v", len(results), results)
	}
	item := results[0].Item
	if item.SourceType != "eval" {
		t.Fatalf("source_type=%q want eval", item.SourceType)
	}
	if item.SourceID != "traj-ok:data_correctness" {
		t.Fatalf("source_id=%q want traj-ok:data_correctness", item.SourceID)
	}
	if item.TaskID != "big_r_retention" {
		t.Fatalf("task_id=%q want big_r_retention", item.TaskID)
	}
	if strings.Contains(item.AnswerOutline, "{") || strings.Contains(item.AnswerOutline, "player_basics") {
		t.Fatalf("answer outline should summarize tool path, got %q", item.AnswerOutline)
	}
}

func TestIngestManualNotes(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	notes := strings.NewReader(`
notes:
  - id: whale-retention
    task_id: big_r_retention
    question: "How should whale retention be analyzed?"
    summary: "Define whales, then inspect retention by server and level."
    answer_outline: "Use analyze with group_by server_id."
    tools: ["analyze"]
    tags: ["retention", "whale"]
    score: 0.95
`)
	report, err := IngestManualNotes(ctx, store, notes, ManualOptions{Adapter: "b3"})
	if err != nil {
		t.Fatalf("ingest manual notes: %v", err)
	}
	if report.Inserted != 1 || report.Skipped != 0 {
		t.Fatalf("report=%+v want inserted=1 skipped=0", report)
	}
	results, err := store.Search(ctx, Query{
		Adapter:  "b3",
		TaskID:   "big_r_retention",
		Question: "whale retention analyze",
		Limit:    5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Item.SourceType != "manual" {
		t.Fatalf("unexpected manual result: %#v", results)
	}
}

func TestIngestManualNotesIsSourceIdempotent(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	body := `
notes:
  - id: repeated-note
    question: "How should activation be analyzed?"
    summary: "Use analyze for activation cohorts."
    tools: ["analyze"]
    tags: ["activation"]
`
	for i := 0; i < 2; i++ {
		report, err := IngestManualNotes(ctx, store, strings.NewReader(body), ManualOptions{Adapter: "b3"})
		if err != nil {
			t.Fatalf("ingest manual notes %d: %v", i, err)
		}
		if report.Inserted != 1 {
			t.Fatalf("inserted=%d want 1", report.Inserted)
		}
	}

	var count int
	err := store.db.QueryRowContext(ctx,
		`SELECT count(*) FROM memory_items WHERE source_type = 'manual' AND source_id = 'repeated-note'`,
	).Scan(&count)
	if err != nil {
		t.Fatalf("count manual note rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("manual note row count=%d want 1", count)
	}
}

func newTrajectoryDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := trajectory.Migrate(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func insertTrajectory(t *testing.T, db *sql.DB, id, taskClass, outcome, question, final string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO trajectories
		(trajectory_id, created_at, agent_version, input_question, final_output, outcome, task_class)
		VALUES (?, 100, 'test', ?, ?, ?, ?)`, id, question, final, outcome, taskClass)
	if err != nil {
		t.Fatal(err)
	}
}

func insertStep(t *testing.T, db *sql.DB, trajID string, idx int, typ, tool, input, output string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO trajectory_steps
		(step_id, trajectory_id, step_index, step_type, started_at, ended_at, tool_name, input, output)
		VALUES (?, ?, ?, ?, 100, 101, ?, ?, ?)`,
		trajID+"-step", trajID, idx, typ, tool, input, output)
	if err != nil {
		t.Fatal(err)
	}
}

func insertEval(t *testing.T, db *sql.DB, trajID, taskID, evaluator string, pass int, value float64) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO eval_results
		(result_id, trajectory_id, task_id, evaluator_name, value, pass, display, created_at)
		VALUES (?, ?, ?, ?, ?, ?, 'ok', 100)`,
		trajID+"-"+evaluator, trajID, taskID, evaluator, value, pass)
	if err != nil {
		t.Fatal(err)
	}
}
