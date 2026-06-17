package memory

import (
	"database/sql"
	_ "embed"
	"fmt"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

// SchemaVersion bumped to "2" when search_text (CJK bigram FTS column) was added.
const SchemaVersion = "2"

// Open 打开 memory.db 并设置适合本地长期记忆读写的 SQLite pragmas。
func Open(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA synchronous=NORMAL",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("pragma %q: %w", pragma, err)
		}
	}
	return db, nil
}

// Migrate initializes memory.db and verifies that its schema version is current.
func Migrate(db *sql.DB) error {
	if _, err := db.Exec(schemaSQL); err != nil {
		return fmt.Errorf("create memory schema: %w", err)
	}

	var version string
	err := db.QueryRow(`SELECT value FROM memory_meta WHERE key='schema_version'`).Scan(&version)
	switch {
	case err == sql.ErrNoRows:
		_, err = db.Exec(`INSERT INTO memory_meta(key, value) VALUES('schema_version', ?)`, SchemaVersion)
		if err != nil {
			return fmt.Errorf("write memory schema_version: %w", err)
		}
		return nil
	case err != nil:
		return fmt.Errorf("read memory schema_version: %w", err)
	case version == SchemaVersion:
		return nil
	default:
		return fmt.Errorf("memory schema version mismatch: db=%s code=%s; rebuild memory.db", version, SchemaVersion)
	}
}
