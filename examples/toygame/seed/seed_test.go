package seed

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestSeed_DeterministicCounts(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "toygame.db")
	schemaPath := "../schema.yaml"

	n, err := Seed(dbPath, schemaPath)
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if n != TotalPlayers {
		t.Fatalf("seeded %d players, want %d", n, TotalPlayers)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	var basics, currencies int64
	db.QueryRow(`SELECT count(*) FROM player_basics`).Scan(&basics)
	db.QueryRow(`SELECT count(*) FROM player_currencies`).Scan(&currencies)
	if basics != TotalPlayers || currencies != TotalPlayers {
		t.Fatalf("basics=%d currencies=%d, want %d each", basics, currencies, TotalPlayers)
	}

	var ver string
	if err := db.QueryRow(`SELECT value FROM _meta WHERE key='schema_version'`).Scan(&ver); err != nil {
		t.Fatalf("_meta.schema_version missing: %v", err)
	}
	if ver != "1" {
		t.Fatalf("schema_version=%s want 1", ver)
	}
}
