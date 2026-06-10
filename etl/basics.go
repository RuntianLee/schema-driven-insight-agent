package etl

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/RuntianLee/schema-driven-insight-agent/schema_protocol"

	"github.com/jackc/pgx/v5"
	_ "modernc.org/sqlite"
)

// BasicsCol 是 state 表在 Layer2 物化的一列。
type BasicsCol struct {
	Name       string
	SQLiteType string // INTEGER | TEXT
}

// BasicsColumns 从 schema 推导给定 state 表的非-PII 物化列（确定性字母序）。
func BasicsColumns(s *schema_protocol.Schema, table string) ([]BasicsCol, error) {
	tbl, ok := s.StateTables[table]
	if !ok {
		return nil, fmt.Errorf("schema missing state_table %s", table)
	}
	var cols []BasicsCol
	for name, fd := range tbl.Fields {
		if fd.PII || fd.OmitInLayer2 {
			continue
		}
		cols = append(cols, BasicsCol{Name: name, SQLiteType: sqliteAffinity(fd.Type)})
	}
	if len(cols) == 0 {
		return nil, fmt.Errorf("%s has no non-PII columns", table)
	}
	sort.Slice(cols, func(i, j int) bool { return cols[i].Name < cols[j].Name })
	return cols, nil
}

// sqliteAffinity：string → TEXT；其余（int*/unix_timestamp_seconds）→ INTEGER。
func sqliteAffinity(pgType string) string {
	if pgType == "string" {
		return "TEXT"
	}
	return "INTEGER"
}

func colNames(cols []BasicsCol) []string {
	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = c.Name
	}
	return names
}

// ExtractBasics：只读连生产 PG，按给定列泛型拉取（cols 来自 schema 可信源，安全拼入）。
func ExtractBasics(ctx context.Context, pgDSN, table string, cols []string, where string, args []any) ([][]any, error) {
	conn, err := pgx.Connect(ctx, pgDSN)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)

	if _, err := conn.Exec(ctx, "SET statement_timeout = '5min'"); err != nil {
		return nil, fmt.Errorf("set statement_timeout: %w", err)
	}
	if _, err := conn.Exec(ctx, "SET default_transaction_read_only = on"); err != nil {
		return nil, fmt.Errorf("set default_transaction_read_only: %w", err)
	}

	q := buildSelectQuery(table, cols, where)
	rows, err := conn.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var out [][]any
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, fmt.Errorf("values: %w", err)
		}
		out = append(out, vals)
	}
	return out, rows.Err()
}

// LoadBasics：tx 全量替换；DDL/INSERT 由 cols 动态构造；indexCols 仅对已物化列建索引。
func LoadBasics(rows [][]any, cols []BasicsCol, table, sqlitePath string, indexCols []string) error {
	db, err := sql.Open("sqlite", sqlitePath)
	if err != nil {
		return err
	}
	defer db.Close()

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(fmt.Sprintf(`DROP TABLE IF EXISTS %s`, table)); err != nil {
		return err
	}
	colDefs := make([]string, len(cols))
	for i, c := range cols {
		colDefs[i] = c.Name + " " + c.SQLiteType
	}
	if _, err := tx.Exec(fmt.Sprintf(`CREATE TABLE %s (%s)`, table, strings.Join(colDefs, ", "))); err != nil {
		return err
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(cols)), ",")
	stmt, err := tx.Prepare(fmt.Sprintf(`INSERT INTO %s VALUES (%s)`, table, ph))
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, r := range rows {
		if _, err := stmt.Exec(r...); err != nil {
			return err
		}
	}
	present := make(map[string]bool, len(cols))
	for _, c := range cols {
		present[c.Name] = true
	}
	for _, c := range indexCols {
		if present[c] {
			if _, err := tx.Exec(fmt.Sprintf(`CREATE INDEX idx_%s_%s ON %s(%s)`, table, c, table, c)); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
