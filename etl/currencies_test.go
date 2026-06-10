package etl

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestLoadCurrencies_WritesRowsAndMeta(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	rows := []CurrencyRow{
		{"h1", "basic", 100}, {"h1", "premium", 5}, {"h1", "virtual", 7},
	}
	if err := LoadCurrencies(rows, "player_currencies", path, 3); err != nil {
		t.Fatalf("LoadCurrencies: %v", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var n int
	if err := db.QueryRow(`SELECT count(*) FROM player_currencies`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("row count = %d, want 3", n)
	}
	var ver string
	if err := db.QueryRow(`SELECT value FROM _meta WHERE key='schema_version'`).Scan(&ver); err != nil {
		t.Fatal(err)
	}
	if ver != "3" {
		t.Errorf("_meta schema_version = %q, want \"3\"", ver)
	}
}
