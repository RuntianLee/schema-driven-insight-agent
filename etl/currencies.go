package etl

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"
	_ "modernc.org/sqlite"
)

// MoneyColumn 把源表的一个货币列映射到派生 currency_type。
type MoneyColumn struct {
	Column       string // 源 PG 列名，如 "basic_money"
	CurrencyType string // 派生类型标签，如 "basic"
}

// CurrencyRow 是派生 player_currencies 的一行（player_id 已脱敏 hash）。
type CurrencyRow struct {
	PlayerID     string
	CurrencyType string
	Balance      int64
}

// ExtractCurrencies：只读连 PG，pivot 给定货币列 → 行（含脱敏 hash）。
// 全部货币列按 int64 读取（int32 列由 PG 驱动安全提升）。
func ExtractCurrencies(ctx context.Context, pgDSN, srcTable, pidCol, salt string, money []MoneyColumn, where string, args []any) ([]CurrencyRow, error) {
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

	cols := make([]string, 0, len(money)+1)
	cols = append(cols, pidCol)
	for _, m := range money {
		cols = append(cols, m.Column)
	}
	q := buildSelectQuery(srcTable, cols, where)
	pgRows, err := conn.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer pgRows.Close()

	var out []CurrencyRow
	for pgRows.Next() {
		vals, err := pgRows.Values()
		if err != nil {
			return nil, fmt.Errorf("values: %w", err)
		}
		pid, ok := toInt64(vals[0])
		if !ok {
			return nil, fmt.Errorf("pid column %q not integer: %T", pidCol, vals[0])
		}
		h := HashPID(salt, pid)
		for i, m := range money {
			bal, ok := toInt64(vals[i+1])
			if !ok {
				return nil, fmt.Errorf("money column %q not integer: %T", m.Column, vals[i+1])
			}
			out = append(out, CurrencyRow{h, m.CurrencyType, bal})
		}
	}
	return out, pgRows.Err()
}

// LoadCurrencies：tx 全量替换 + 索引 + 戳 _meta.schema_version；崩溃则旧库无损。
func LoadCurrencies(rows []CurrencyRow, destTable, sqlitePath string, schemaVersion int) error {
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

	if _, err := tx.Exec(fmt.Sprintf(`DROP TABLE IF EXISTS %s`, destTable)); err != nil {
		return err
	}
	if _, err := tx.Exec(fmt.Sprintf(`CREATE TABLE %s (player_id TEXT, currency_type TEXT, balance INTEGER)`, destTable)); err != nil {
		return err
	}
	stmt, err := tx.Prepare(fmt.Sprintf(`INSERT INTO %s VALUES (?, ?, ?)`, destTable))
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, r := range rows {
		if _, err := stmt.Exec(r.PlayerID, r.CurrencyType, r.Balance); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(fmt.Sprintf(`CREATE INDEX idx_%s ON %s(currency_type, balance)`, destTable, destTable)); err != nil {
		return err
	}
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS _meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT OR REPLACE INTO _meta (key, value) VALUES ('schema_version', ?)`, strconv.Itoa(schemaVersion)); err != nil {
		return err
	}
	return tx.Commit()
}

// toInt64 把 pgx 返回的整型值（int16/int32/int64）统一为 int64。
func toInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case int32:
		return int64(n), true
	case int16:
		return int64(n), true
	default:
		return 0, false
	}
}
