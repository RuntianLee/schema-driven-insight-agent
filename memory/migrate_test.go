package memory

import (
	"database/sql"
	"path/filepath"
	"strings"
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
			('m1', 'manual', 'n1', 'b3', 'retention', 'benchmark', 'query retention',
			 'retention analysis should segment payers before activity.', 'Use analyze grouped aggregation.', '["analyze"]', '["retention"]', 1.0, 100, 100)`)
	if err != nil {
		t.Fatalf("insert memory item: %v", err)
	}

	var id string
	err = db.QueryRow(`
		SELECT memory_items.item_id
		FROM memory_items_fts
		JOIN memory_items ON memory_items_fts.rowid = memory_items.rowid
		WHERE memory_items_fts MATCH 'retention'`).Scan(&id)
	if err != nil {
		t.Fatalf("fts search: %v", err)
	}
	if id != "m1" {
		t.Fatalf("id=%q want m1", id)
	}

	var snippet string
	err = db.QueryRow(`
		SELECT snippet(memory_items_fts, 1, '[', ']', '...', 12)
		FROM memory_items_fts
		WHERE memory_items_fts MATCH 'retention'`).Scan(&snippet)
	if err != nil {
		t.Fatalf("fts snippet: %v", err)
	}
	if !strings.Contains(snippet, "[retention]") {
		t.Fatalf("snippet=%q, want highlighted retention", snippet)
	}
}

func TestMigrateFTSUpdateAndDeleteSync(t *testing.T) {
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
			 answer_outline, tools_json, tags_json, score, created_at, updated_at)
		VALUES
			('m3', 'manual', 'n3', 'b3', 'old retention question',
			 'legacy retention metric', 'retention outline', '["analyze"]', '["retention"]', 0.7, 102, 102)`)
	if err != nil {
		t.Fatalf("insert memory item: %v", err)
	}

	_, err = db.Exec(`
		UPDATE memory_items
		SET question='new activation question',
			summary='activation metric',
			answer_outline='activation outline',
			tags_json='["activation"]',
			updated_at=103
		WHERE item_id='m3'`)
	if err != nil {
		t.Fatalf("update memory item: %v", err)
	}
	if got := ftsCount(t, db, "retention"); got != 0 {
		t.Fatalf("old token count=%d want 0", got)
	}
	if got := ftsCount(t, db, "activation"); got != 1 {
		t.Fatalf("new token count=%d want 1", got)
	}

	_, err = db.Exec(`DELETE FROM memory_items WHERE item_id='m3'`)
	if err != nil {
		t.Fatalf("delete memory item: %v", err)
	}
	if got := ftsCount(t, db, "activation"); got != 0 {
		t.Fatalf("deleted token count=%d want 0", got)
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
			('m2', 'manual', 'n2', 'b3', 'optional fields',
			 'optional fields still index retention text.', '[]', '[]', 0.5, 101, 101)`)
	if err != nil {
		t.Fatalf("insert memory item with optional fields: %v", err)
	}

	var id string
	err = db.QueryRow(`
		SELECT memory_items.item_id
		FROM memory_items_fts
		JOIN memory_items ON memory_items_fts.rowid = memory_items.rowid
		WHERE memory_items_fts MATCH 'retention'`).Scan(&id)
	if err != nil {
		t.Fatalf("fts search optional item: %v", err)
	}
	if id != "m2" {
		t.Fatalf("id=%q want m2", id)
	}
}

func TestMigrateAllowsReflectionSourceType(t *testing.T) {
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
			('reflection-1', 'reflection', 'r1', 'b3', 'retention', 'benchmark',
			 '如何分析留存', '先确认 cohort 和过滤口径。', '优先校验过滤和分组。',
			 '["analyze"]', '["reflection","fix-query"]', 0.8, 100, 100)`)
	if err != nil {
		t.Fatalf("reflection source_type should be accepted: %v", err)
	}
}

func ftsCount(t *testing.T, db *sql.DB, query string) int {
	t.Helper()
	var got int
	err := db.QueryRow(`SELECT count(*) FROM memory_items_fts WHERE memory_items_fts MATCH ?`, query).Scan(&got)
	if err != nil {
		t.Fatalf("fts count %q: %v", query, err)
	}
	return got
}

func assertTableExists(t *testing.T, db *sql.DB, name string) {
	t.Helper()
	var got string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type IN ('table','virtual table') AND name=?`, name).Scan(&got)
	if err != nil {
		t.Fatalf("table %s missing: %v", name, err)
	}
}
