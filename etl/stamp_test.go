package etl

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestMaxColumnAndWriteDataAsOf(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "t.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE pb (last_online INTEGER); INSERT INTO pb VALUES (100),(300),(200)`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	got, err := MaxColumn(dbPath, "pb", "last_online")
	if err != nil || got != 300 {
		t.Fatalf("MaxColumn got %d, %v; want 300", got, err)
	}
	if err := WriteDataAsOf(dbPath, 300); err != nil {
		t.Fatal(err)
	}
	db, _ = sql.Open("sqlite", dbPath)
	defer db.Close()
	var v string
	if err := db.QueryRow(`SELECT value FROM _meta WHERE key='data_as_of'`).Scan(&v); err != nil || v != "300" {
		t.Errorf("_meta.data_as_of got %q, %v; want 300", v, err)
	}
}

func TestMaxColumn_EmptyTableIsZero(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "t.db")
	db, _ := sql.Open("sqlite", dbPath)
	db.Exec(`CREATE TABLE pb (last_online INTEGER)`)
	db.Close()
	got, err := MaxColumn(dbPath, "pb", "last_online")
	if err != nil || got != 0 {
		t.Errorf("空表应返回 0, got %d, %v", got, err)
	}
}
