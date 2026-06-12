package introspect

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Introspect 只读内省 public schema 下指定表的列与 PK。
// 与 ETL 同保护阶梯：statement_timeout + 会话级 default_transaction_read_only。
// SECURITY: dsn 绝不打印；输出仅含元数据（无行数据）。
func Introspect(ctx context.Context, dsn string, tables []string) ([]TableInfo, error) {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, "SET statement_timeout = '1min'"); err != nil {
		return nil, fmt.Errorf("set statement_timeout: %w", err)
	}
	if _, err := conn.Exec(ctx, "SET default_transaction_read_only = on"); err != nil {
		return nil, fmt.Errorf("set default_transaction_read_only: %w", err)
	}

	pks := map[string]map[string]bool{}
	pkRows, err := conn.Query(ctx, `
		SELECT kcu.table_name, kcu.column_name
		FROM information_schema.table_constraints tc
		JOIN information_schema.key_column_usage kcu
		  ON tc.constraint_name = kcu.constraint_name AND tc.table_schema = kcu.table_schema
		WHERE tc.constraint_type = 'PRIMARY KEY' AND tc.table_schema = 'public'
		  AND kcu.table_name = ANY($1)`, tables)
	if err != nil {
		return nil, fmt.Errorf("query pk: %w", err)
	}
	for pkRows.Next() {
		var tbl, col string
		if err := pkRows.Scan(&tbl, &col); err != nil {
			return nil, err
		}
		if pks[tbl] == nil {
			pks[tbl] = map[string]bool{}
		}
		pks[tbl][col] = true
	}
	if err := pkRows.Err(); err != nil {
		return nil, err
	}

	rows, err := conn.Query(ctx, `
		SELECT table_name, column_name, data_type
		FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = ANY($1)
		ORDER BY table_name, ordinal_position`, tables)
	if err != nil {
		return nil, fmt.Errorf("query columns: %w", err)
	}
	defer rows.Close()

	byTable := map[string]*TableInfo{}
	var order []string
	for rows.Next() {
		var tbl, col, typ string
		if err := rows.Scan(&tbl, &col, &typ); err != nil {
			return nil, err
		}
		ti, ok := byTable[tbl]
		if !ok {
			ti = &TableInfo{Name: tbl}
			byTable[tbl] = ti
			order = append(order, tbl)
		}
		ti.Columns = append(ti.Columns, Column{Name: col, PGType: typ, IsPK: pks[tbl][col]})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(order) == 0 {
		return nil, fmt.Errorf("public schema 下未找到表 %v（确认表名与权限）", tables)
	}
	var missing []string
	for _, want := range tables {
		if _, ok := byTable[want]; !ok {
			missing = append(missing, want)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("表不存在或无权限: %v", missing)
	}
	out := make([]TableInfo, 0, len(order))
	for _, t := range order {
		out = append(out, *byTable[t])
	}
	return out, nil
}
