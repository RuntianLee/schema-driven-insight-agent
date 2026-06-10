package main

import (
	"database/sql"
	"fmt"
	"net/url"
	"testing"

	_ "modernc.org/sqlite"
)

func openMemDB(t *testing.T) *sql.DB {
	t.Helper()
	// Each subtest gets its own named in-memory database so that shared-cache
	// connections from a previous subtest cannot bleed into the next one.
	// t.Name() is unique per subtest, so no two subtests share a connection pool.
	dbName := url.PathEscape(t.Name())
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open mem db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestCheckBizSchemaVersion(t *testing.T) {
	t.Run("no _meta table → error", func(t *testing.T) {
		db := openMemDB(t)
		if err := checkBizSchemaVersion(db, 2); err == nil {
			t.Fatal("expected error when _meta table is absent")
		}
	})

	t.Run("matching version → nil", func(t *testing.T) {
		db := openMemDB(t)
		_, err := db.Exec(`CREATE TABLE _meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`)
		if err != nil {
			t.Fatalf("create _meta: %v", err)
		}
		if _, err := db.Exec(`INSERT INTO _meta VALUES ('schema_version','2')`); err != nil {
			t.Fatalf("insert: %v", err)
		}
		if err := checkBizSchemaVersion(db, 2); err != nil {
			t.Fatalf("want nil, got: %v", err)
		}
	})

	t.Run("mismatched version → error", func(t *testing.T) {
		db := openMemDB(t)
		_, err := db.Exec(`CREATE TABLE _meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`)
		if err != nil {
			t.Fatalf("create _meta: %v", err)
		}
		if _, err := db.Exec(`INSERT INTO _meta VALUES ('schema_version','1')`); err != nil {
			t.Fatalf("insert: %v", err)
		}
		if err := checkBizSchemaVersion(db, 2); err == nil {
			t.Fatal("expected error for version mismatch")
		}
	})
}
