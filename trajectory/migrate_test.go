package trajectory

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// v1 schema（task_class 列与 eval_results 表均无）——模拟存量旧库。
const v1DDL = `
CREATE TABLE trajectories (
	trajectory_id TEXT PRIMARY KEY, created_at INTEGER NOT NULL,
	agent_version TEXT NOT NULL, input_question TEXT NOT NULL,
	final_output TEXT, outcome TEXT, total_tokens INTEGER, total_cost_usd REAL,
	total_latency_ms INTEGER, step_count INTEGER, error_summary TEXT, metadata TEXT
);
CREATE TABLE trajectory_steps (
	step_id TEXT PRIMARY KEY, trajectory_id TEXT NOT NULL, step_index INTEGER NOT NULL,
	step_type TEXT NOT NULL, started_at INTEGER NOT NULL, ended_at INTEGER NOT NULL,
	latency_ms INTEGER, input TEXT, output TEXT, tokens_input INTEGER, tokens_output INTEGER,
	cost_usd REAL, model_name TEXT, tool_name TEXT, error TEXT, metadata TEXT
);
CREATE TABLE _meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
INSERT INTO _meta(key,value) VALUES('schema_version','1');
INSERT INTO trajectories(trajectory_id,created_at,agent_version,input_question,outcome)
VALUES('old-1', 1700000000, 'v0.1.0', '历史问题', 'success');
`

func TestMigrate_V1ToV2(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v1.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(v1DDL); err != nil {
		t.Fatalf("seed v1: %v", err)
	}

	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate v1→v2: %v", err)
	}

	var v string
	db.QueryRow(`SELECT value FROM _meta WHERE key='schema_version'`).Scan(&v)
	if v != "2" {
		t.Errorf("schema_version = %q, want 2", v)
	}
	if !hasColumn(db, "trajectories", "task_class") {
		t.Error("task_class column missing after migrate")
	}
	var tc sql.NullString
	db.QueryRow(`SELECT task_class FROM trajectories WHERE trajectory_id='old-1'`).Scan(&tc)
	if tc.Valid {
		t.Errorf("old row task_class should be NULL, got %q", tc.String)
	}
	if _, err := db.Exec(`INSERT INTO eval_results(result_id,trajectory_id,task_id,evaluator_name,value,pass,display,created_at)
		VALUES('r1','old-1','t1','data_correctness',1.0,1,'1.00 ✓',1700000001)`); err != nil {
		t.Errorf("eval_results insert failed (table missing?): %v", err)
	}
	var n int
	db.QueryRow(`SELECT count(*) FROM trajectories`).Scan(&n)
	if n != 1 {
		t.Errorf("existing trajectory lost: count=%d", n)
	}
}

func TestMigrate_FreshDBIsV2(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "fresh.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate fresh: %v", err)
	}
	var v string
	db.QueryRow(`SELECT value FROM _meta WHERE key='schema_version'`).Scan(&v)
	if v != "2" {
		t.Errorf("fresh db schema_version = %q, want 2", v)
	}
	if !hasColumn(db, "trajectories", "task_class") {
		t.Error("fresh db missing task_class")
	}
}

func TestMigrate_Idempotent(t *testing.T) {
	db, _ := Open(filepath.Join(t.TempDir(), "i.db"))
	defer db.Close()
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("second Migrate not idempotent: %v", err)
	}
}
