// stamp.go：快照"有效现在"（data_as_of）的读写。
// 源自 td adapter 的 StampDataAsOf，通用化为 表/列 参数（列名来自 schema role=last_seen，
// 经 parser reIdent 校验，可安全拼入 SQL）。
package etl

import (
	"database/sql"
	"fmt"
	"strconv"

	_ "modernc.org/sqlite"
)

// MaxColumn 读 Layer2 表某列的 MAX（NULL/空表 → 0）。
func MaxColumn(sqlitePath, table, column string) (int64, error) {
	db, err := sql.Open("sqlite", sqlitePath)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	var v int64
	q := fmt.Sprintf(`SELECT COALESCE(MAX(%s), 0) FROM %s`, column, table)
	if err := db.QueryRow(q).Scan(&v); err != nil {
		return 0, fmt.Errorf("query max %s.%s: %w", table, column, err)
	}
	return v, nil
}

// WriteDataAsOf 把快照"有效现在"写入 _meta.data_as_of（供运营问题构造绝对 cutoff）。
func WriteDataAsOf(sqlitePath string, asOf int64) error {
	db, err := sql.Open("sqlite", sqlitePath)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS _meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		return fmt.Errorf("create _meta table: %w", err)
	}
	if _, err := db.Exec(`INSERT OR REPLACE INTO _meta (key, value) VALUES ('data_as_of', ?)`,
		strconv.FormatInt(asOf, 10)); err != nil {
		return fmt.Errorf("write _meta.data_as_of: %w", err)
	}
	return nil
}
