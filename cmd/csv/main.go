// Command csv 把一份 CSV 文件构建成 de-identified Layer-2 快照 + health
// （零代码接入：手里有 CSV / 导出报表的统一入口，无真库即可体验 agent/eval 全流程）。
//
// 用法：
//
//	go run github.com/RuntianLee/schema-driven-insight-agent/cmd/csv \
//	  -schema examples/bankchurn/schema.yaml
//
// CSV 视作 Layer-1 原始源（含 PII）；actor_id 经 HashPID 脱敏、omit_in_layer2 列不入库。
package main

import (
	"flag"
	"log"

	"github.com/RuntianLee/schema-driven-insight-agent/etl/csvload"

	_ "modernc.org/sqlite"
)

func main() {
	schemaPath := flag.String("schema", "schema.yaml", "schema.yaml 路径")
	csvPath := flag.String("csv", "", "CSV 路径（空=schema data_sources type=csv 的 path）")
	sqlitePath := flag.String("sqlite", "", "Layer2 SQLite 输出路径（空=schema data_sources.layer2.path）")
	healthPath := flag.String("health", "", "etl_health.json 输出路径（空=etl_policy.health_path 或 db 同目录）")
	flag.Parse()

	if err := csvload.Run(csvload.RunOptions{
		SchemaPath: *schemaPath, CSVPath: *csvPath,
		SQLitePath: *sqlitePath, HealthPath: *healthPath,
	}); err != nil {
		log.Fatalf("csv failed: %v", err)
	}
	log.Printf("csv OK")
}
