package trajectory

import (
	"database/sql"
	_ "embed"
	"fmt"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

const SchemaVersion = "2"

// Open 打开 trajectory.db 并设 WAL 连接参数（trajectory-spec-v2 §3.1）。
func Open(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	for _, p := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA synchronous=NORMAL",
	} {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, fmt.Errorf("pragma %q: %w", p, err)
		}
	}
	return db, nil
}

// Migrate 建表并把存量库升到当前 schema_version（trajectory-spec-v2 §10）。
func Migrate(db *sql.DB) error {
	if _, err := db.Exec(schemaSQL); err != nil {
		return fmt.Errorf("create tables: %w", err)
	}
	var v string
	err := db.QueryRow(`SELECT value FROM _meta WHERE key='schema_version'`).Scan(&v)
	switch {
	case err == sql.ErrNoRows:
		_, err = db.Exec(`INSERT INTO _meta(key, value) VALUES('schema_version', ?)`, SchemaVersion)
		return err
	case err != nil:
		return err
	case v == SchemaVersion:
		return nil
	case v == "1":
		return migrateV1toV2(db)
	default:
		return fmt.Errorf("trajectory schema version mismatch: db=%s code=%s; run migrate", v, SchemaVersion)
	}
}

// migrateV1toV2：给存量 trajectories 补 task_class 列（eval_results 已由 schemaSQL 的
// CREATE IF NOT EXISTS 建出），再 bump 版本。事务包裹：ALTER + bump 原子提交，避免
// 中途崩溃留下"有列但版本仍为 1"的中间态。幂等：列已存在则跳过 ALTER（SQLite 支持
// 事务内 DDL）。
func migrateV1toV2(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin migrate v1→v2: %w", err)
	}
	defer tx.Rollback()

	if !hasColumn(db, "trajectories", "task_class") {
		if _, err := tx.Exec(`ALTER TABLE trajectories ADD COLUMN task_class TEXT`); err != nil {
			return fmt.Errorf("add task_class: %w", err)
		}
	}
	if _, err := tx.Exec(`UPDATE _meta SET value=? WHERE key='schema_version'`, SchemaVersion); err != nil {
		return fmt.Errorf("bump schema_version: %w", err)
	}
	return tx.Commit()
}

// hasColumn 用 PRAGMA table_info 判断列是否存在。
// table 由调用方以硬编码字面量传入（PRAGMA 不支持 ? 占位符，故拼接；无用户输入风险）。
func hasColumn(db *sql.DB, table, col string) bool {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false
		}
		if name == col {
			return true
		}
	}
	return false
}
