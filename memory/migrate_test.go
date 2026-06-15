package memory

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestMigrateCreatesSchemaAndIsIdempotent(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := Migrate(db); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("second migrate: %v", err)
	}

	var version string
	if err := db.QueryRow(`SELECT value FROM memory_meta WHERE key='schema_version'`).Scan(&version); err != nil {
		t.Fatalf("schema version: %v", err)
	}
	if version != SchemaVersion {
		t.Fatalf("schema version=%q want %q", version, SchemaVersion)
	}

	assertTableExists(t, db, "memory_items")
	assertTableExists(t, db, "memory_items_fts")
}

func TestMigrateFTSCanSearchInsertedContent(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}

	_, err = db.Exec(`
		INSERT INTO memory_items
			(item_id, source_type, source_id, adapter, task_id, task_class, question,
			 summary, answer_outline, tools_json, tags_json, score, created_at, updated_at)
		VALUES
			('m1', 'manual', 'n1', 'b3', 'retention', 'benchmark', '查询大R留存',
			 '大R留存需要先按付费分层再看活跃。', '使用 analyze 分组聚合。', '["analyze"]', '["retention"]', 1.0, 100, 100)`)
	if err != nil {
		t.Fatalf("insert memory item: %v", err)
	}

	var id string
	err = db.QueryRow(`
		SELECT memory_items.item_id
		FROM memory_items_fts
		JOIN memory_items ON memory_items_fts.rowid = memory_items.rowid
		WHERE memory_items_fts MATCH '留存'`).Scan(&id)
	if err != nil {
		t.Fatalf("fts search: %v", err)
	}
	if id != "m1" {
		t.Fatalf("id=%q want m1", id)
	}
}

func TestMigrateAllowsOptionalFields(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}

	_, err = db.Exec(`
		INSERT INTO memory_items
			(item_id, source_type, source_id, adapter, question, summary,
			 tools_json, tags_json, score, created_at, updated_at)
		VALUES
			('m2', 'manual', 'n2', 'b3', '可选字段测试',
			 '可选字段为空时也需要进入全文索引。', '[]', '[]', 0.5, 101, 101)`)
	if err != nil {
		t.Fatalf("insert memory item with optional fields: %v", err)
	}

	var id string
	err = db.QueryRow(`
		SELECT memory_items.item_id
		FROM memory_items_fts
		JOIN memory_items ON memory_items_fts.rowid = memory_items.rowid
		WHERE memory_items_fts MATCH '可选'`).Scan(&id)
	if err != nil {
		t.Fatalf("fts search optional item: %v", err)
	}
	if id != "m2" {
		t.Fatalf("id=%q want m2", id)
	}
}

func assertTableExists(t *testing.T, db *sql.DB, name string) {
	t.Helper()
	var got string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type IN ('table','virtual table') AND name=?`, name).Scan(&got)
	if err != nil {
		t.Fatalf("table %s missing: %v", name, err)
	}
}
