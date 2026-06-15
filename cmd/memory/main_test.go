package main

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RuntianLee/schema-driven-insight-agent/trajectory"
)

func TestRunInitManualAndSearch(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "memory.db")
	notePath := filepath.Join(dir, "notes.yaml")
	writeFile(t, notePath, `notes:
  - id: whale-retention-note
    task_id: retention-task
    question: How should whale retention be analyzed?
    summary: Whale retention analysis should compare cohort survival and repeat activity.
    answer_outline: Use analyze to compute whale retention cohorts.
    tools: [analyze]
    tags: [retention, whale]
    score: 1
`)

	var out bytes.Buffer
	if code := run([]string{"-memory-db", dbPath, "-init"}, &out); code != 0 {
		t.Fatalf("init exit code = %d, want 0; output=%s", code, out.String())
	}
	if !strings.Contains(out.String(), "initialized") {
		t.Fatalf("init output = %q, want initialized", out.String())
	}

	out.Reset()
	if code := run([]string{"-memory-db", dbPath, "-adapter", "b3", "-manual", notePath}, &out); code != 0 {
		t.Fatalf("manual ingest exit code = %d, want 0; output=%s", code, out.String())
	}
	if !strings.Contains(out.String(), "manual ingest inserted=1") {
		t.Fatalf("manual ingest output = %q, want inserted count", out.String())
	}

	out.Reset()
	if code := run([]string{"-memory-db", dbPath, "-adapter", "b3", "-search", "whale retention analyze"}, &out); code != 0 {
		t.Fatalf("search exit code = %d, want 0; output=%s", code, out.String())
	}
	if !strings.Contains(out.String(), "Whale retention analysis should compare cohort survival") {
		t.Fatalf("search output = %q, want manual note summary", out.String())
	}
}

func TestRunRequiresAction(t *testing.T) {
	var out bytes.Buffer
	if code := run([]string{}, &out); code != 2 {
		t.Fatalf("exit code = %d, want 2; output=%s", code, out.String())
	}
}

func TestRunIngestRequiresAdapter(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "memory.db")
	notePath := filepath.Join(dir, "notes.yaml")
	writeFile(t, notePath, "notes: []\n")

	var out bytes.Buffer
	if code := run([]string{"-memory-db", dbPath, "-manual", notePath}, &out); code != 2 {
		t.Fatalf("manual without adapter exit code = %d, want 2; output=%s", code, out.String())
	}

	out.Reset()
	if code := run([]string{"-memory-db", dbPath, "-trajectory-db", filepath.Join(dir, "trajectory.db")}, &out); code != 2 {
		t.Fatalf("trajectory without adapter exit code = %d, want 2; output=%s", code, out.String())
	}
}

func TestRunTrajectoryIngestMigratesTrajectoryDB(t *testing.T) {
	dir := t.TempDir()
	memoryPath := filepath.Join(dir, "memory.db")
	trajectoryPath := filepath.Join(dir, "trajectory.db")
	seedTrajectoryDB(t, trajectoryPath)

	var out bytes.Buffer
	if code := run([]string{"-memory-db", memoryPath, "-adapter", "b3", "-trajectory-db", trajectoryPath}, &out); code != 0 {
		t.Fatalf("trajectory ingest exit code = %d, want 0; output=%s", code, out.String())
	}
	if !strings.Contains(out.String(), "trajectory ingest inserted=1 skipped=0") {
		t.Fatalf("trajectory ingest output = %q, want inserted count", out.String())
	}

	out.Reset()
	if code := run([]string{"-memory-db", memoryPath, "-adapter", "b3", "-search", "retention analyze"}, &out); code != 0 {
		t.Fatalf("search exit code = %d, want 0; output=%s", code, out.String())
	}
	if !strings.Contains(out.String(), "Use analyze for retention cohorts") {
		t.Fatalf("search output = %q, want ingested trajectory summary", out.String())
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func seedTrajectoryDB(t *testing.T, path string) {
	t.Helper()
	db, err := trajectory.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := trajectory.Migrate(db); err != nil {
		t.Fatal(err)
	}
	insertTrajectory(t, db, "traj-cli", "benchmark", "success",
		"How should retention be analyzed?",
		"Use analyze for retention cohorts.")
	insertStep(t, db, "traj-cli", 0, "analyze")
	insertEval(t, db, "traj-cli", "retention_cli")
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

func insertStep(t *testing.T, db *sql.DB, trajID string, idx int, tool string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO trajectory_steps
		(step_id, trajectory_id, step_index, step_type, started_at, ended_at, tool_name)
		VALUES (?, ?, ?, 'tool_call', 100, 101, ?)`,
		trajID+"-step", trajID, idx, tool)
	if err != nil {
		t.Fatal(err)
	}
}

func insertEval(t *testing.T, db *sql.DB, trajID, taskID string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO eval_results
		(result_id, trajectory_id, task_id, evaluator_name, value, pass, display, created_at)
		VALUES (?, ?, ?, 'data_correctness', 1, 1, 'ok', 100)`,
		trajID+"-eval", trajID, taskID)
	if err != nil {
		t.Fatal(err)
	}
}
