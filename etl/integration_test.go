//go:build integration

// 集成测试（真 Postgres 容器）：RunAll 端到端 + 只读 GUC 回归（I2 教训：
// 必须会话级 default_transaction_read_only，事务级在 autocommit 下即刻回退失效）。
// 本文件接替原 adapters/b3/etl/integration_test.go 的覆盖（adapter Go 删除后移居 framework）。
package etl

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	_ "modernc.org/sqlite"
)

// startPG 起一次性 PG 容器并建样表（结构对齐 run_test.go 的 tdLikeSchema）。
func startPG(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	pgc, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("t"), postgres.WithUsername("u"), postgres.WithPassword("p"),
		postgres.BasicWaitStrategies())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pgc.Terminate(ctx) })
	dsn, err := pgc.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, `
		CREATE TABLE player_basics (
			player_id bigint PRIMARY KEY, server_id integer, level integer,
			basic_money bigint, premium_money bigint, virtual_money integer,
			passed_main_stage_id integer, last_online_time bigint);
		INSERT INTO player_basics
		SELECT g, 1 + g % 2, 10, g*2, g*3, g*5, 100, 1716000000 + g % 100
		FROM generate_series(1, 200) g;`); err != nil {
		t.Fatal(err)
	}
	return dsn
}

func TestIntegration_RunAllEndToEnd(t *testing.T) {
	dsn := startPG(t)
	dir := t.TempDir()
	// 复用 run_test.go 的 tdLikeSchema，但闸门降到测试规模、health 路径默认。
	y := strings.Replace(tdLikeSchema, "min_rows: 5000", "min_rows: 100", 1)
	y = strings.Replace(y, "health_min_rows: 5000", "health_min_rows: 100", 1)
	y = strings.Replace(y, "health_path: ./data/etl_health_td.json", "", 1)
	schemaPath := filepath.Join(dir, "schema.yaml")
	os.WriteFile(schemaPath, []byte(y), 0o644)

	if err := RunAll(context.Background(), RunOptions{SchemaPath: schemaPath, DSN: dsn}); err != nil {
		t.Fatal(err)
	}
	db, _ := sql.Open("sqlite", filepath.Join(dir, "data/td.db"))
	defer db.Close()
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM player_basics`).Scan(&n)
	if n != 200 {
		t.Errorf("basics %d", n)
	}
	db.QueryRow(`SELECT COUNT(*) FROM player_currencies`).Scan(&n)
	if n != 600 {
		t.Errorf("currencies %d（200 玩家 × 3 货币）", n)
	}
	var asOf string
	db.QueryRow(`SELECT value FROM _meta WHERE key='data_as_of'`).Scan(&asOf)
	if asOf != "1716000099" {
		t.Errorf("data_as_of %s", asOf)
	}
}

func TestIntegration_SessionReadOnlyGUC(t *testing.T) {
	// I2 回归：会话级 default_transaction_read_only 必须跨 autocommit 语句持续生效。
	dsn := startPG(t)
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, "SET default_transaction_read_only = on"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, "SELECT 1"); err != nil { // 先消耗一条语句（autocommit 回退陷阱）
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, "INSERT INTO player_basics (player_id) VALUES (9999)"); err == nil {
		t.Fatal("只读会话下 INSERT 应失败")
	}
}
