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
	if v != "3" {
		t.Errorf("schema_version = %q, want 3", v)
	}
	if ok, err := hasColumn(db, "trajectories", "task_class"); err != nil || !ok {
		t.Errorf("task_class column missing after migrate (ok=%v err=%v)", ok, err)
	}
	if ok, err := hasColumn(db, "trajectory_steps", "role"); err != nil || !ok {
		t.Errorf("role column missing after migrate v1→v3 (ok=%v err=%v)", ok, err)
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
	if v != "3" {
		t.Errorf("fresh db schema_version = %q, want 3", v)
	}
	if ok, err := hasColumn(db, "trajectories", "task_class"); err != nil || !ok {
		t.Errorf("fresh db missing task_class (ok=%v err=%v)", ok, err)
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

func TestMigrateV2toV3_AddsRoleColumn(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := Migrate(db); err != nil {
		t.Fatalf("初次 Migrate: %v", err)
	}
	if ok, err := hasColumn(db, "trajectory_steps", "role"); err != nil || !ok {
		t.Fatalf("trajectory_steps 应有 role 列 (ok=%v err=%v)", ok, err)
	}
	var v string
	if err := db.QueryRow(`SELECT value FROM _meta WHERE key='schema_version'`).Scan(&v); err != nil {
		t.Fatalf("读版本: %v", err)
	}
	if v != "3" {
		t.Fatalf("schema_version = %q, want 3", v)
	}
	// 幂等：再迁一次不报错
	if err := Migrate(db); err != nil {
		t.Fatalf("重复 Migrate: %v", err)
	}
}

// TestHasColumn_QueryErrorSurfaced 锁 L-5：hasColumn 此前把查询失败静默归一成 false
//（与"列真不存在"不可区分）。签名改 (bool, error) 后，db 层故障须显式返回 error，
// 不能被调用方误当作"列不存在"处理。
func TestHasColumn_QueryErrorSurfaced(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.Close() // 立即关闭：后续 PRAGMA 查询必然失败
	if _, err := hasColumn(db, "trajectories", "task_class"); err == nil {
		t.Fatal("db 已关闭时 hasColumn 应返回 error，而非静默归一 false")
	}
}

// TestMigrateV1toV2_HasColumnErrorFailsFast 锁 L-5：migrateV1toV2 须显式处理 hasColumn
// 的 error（fail-fast），不能吞掉 db 层故障后继续假定"列不存在"往下走。
func TestMigrateV1toV2_HasColumnErrorFailsFast(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v1-broken.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(v1DDL); err != nil {
		t.Fatalf("seed v1: %v", err)
	}
	db.Close() // 关闭后 hasColumn 的 PRAGMA 查询必然报错
	if err := migrateV1toV2(db); err == nil {
		t.Fatal("db 已关闭时 migrateV1toV2 应 fail-fast 报错，而非吞错继续")
	}
}

func TestMigrateV2toV3_Idempotent_ColumnExists(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(schemaSQL); err != nil {
		t.Fatal(err)
	}
	// 手动置版本为 2，role 列已由 schemaSQL 建出 → migrateV2toV3 应跳过 ALTER、只 bump
	db.Exec(`INSERT INTO _meta(key,value) VALUES('schema_version','2')`)
	if err := migrateV2toV3(db); err != nil {
		t.Fatalf("migrateV2toV3（列已存在）应幂等: %v", err)
	}
}
