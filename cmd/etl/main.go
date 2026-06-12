// Command etl 是 schema 驱动的通用 ETL runner（零代码接入 v0.2）：
// 货币列/索引/data_as_of 全部从 schema.yaml 推导，adapter 无需任何 Go 代码。
//
// 用法：
//
//	go run github.com/RuntianLee/schema-driven-insight-agent/cmd/etl \
//	  -schema ./my-adapter/schema.yaml -db-config ./config/db.yaml
//
// DSN 解析：-db-config 文件存在→严格解析；不存在→schema data_sources 的 dsn_env 兜底。
package main

import (
	"context"
	"flag"
	"log"

	"github.com/RuntianLee/schema-driven-insight-agent/etl"

	_ "modernc.org/sqlite"
)

func main() {
	schemaPath := flag.String("schema", "schema.yaml", "schema.yaml 路径")
	dbConfig := flag.String("db-config", "./config/db.yaml", "PG YAML config（缺省回退 schema dsn_env）")
	sqlitePath := flag.String("sqlite", "", "Layer2 SQLite 输出路径（空=schema data_sources.layer2.path）")
	healthPath := flag.String("health", "", "etl_health.json 输出路径（空=etl_policy.health_path 或 db 同目录）")
	flag.Parse()

	dsnEnv, err := etl.DSNEnvFromSchema(*schemaPath)
	if err != nil {
		log.Fatalf("%v", err)
	}
	// SECURITY: dsn 绝不打印；summary 是非密摘要。
	dsn, summary, err := etl.ResolveDSNFromConfig(*dbConfig, dsnEnv)
	if err != nil {
		log.Fatalf("PG 未配置: %v", err)
	}
	log.Printf("PG: %s", summary)

	if err := etl.RunAll(context.Background(), etl.RunOptions{
		SchemaPath: *schemaPath, DSN: dsn, SQLitePath: *sqlitePath, HealthPath: *healthPath,
	}); err != nil {
		log.Fatalf("ETL failed: %v", err)
	}
	log.Printf("ETL OK")
}
