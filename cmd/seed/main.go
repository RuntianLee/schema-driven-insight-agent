// Command seed 按声明式 seed.yaml 生成确定性合成 Layer2 快照 + health
// （零代码接入 v0.2 的 dev/demo 路径：无真库即可体验 agent/eval 全流程）。
//
// 用法：
//
//	go run github.com/RuntianLee/schema-driven-insight-agent/cmd/seed \
//	  -schema examples/toygame/schema.yaml -spec examples/toygame/seed.yaml
//
// 产物仅限本地开发/演示，勿用于生产。
package main

import (
	"flag"
	"log"

	"github.com/RuntianLee/schema-driven-insight-agent/etl/seedgen"

	_ "modernc.org/sqlite"
)

func main() {
	schemaPath := flag.String("schema", "schema.yaml", "schema.yaml 路径")
	specPath := flag.String("spec", "seed.yaml", "seed.yaml 声明式合成数据 spec")
	sqlitePath := flag.String("sqlite", "", "Layer2 SQLite 输出路径（空=schema data_sources.layer2.path）")
	healthPath := flag.String("health", "", "etl_health.json 输出路径（空=etl_policy.health_path 或 db 同目录）")
	flag.Parse()

	if err := seedgen.Run(seedgen.RunOptions{
		SchemaPath: *schemaPath, SpecPath: *specPath,
		SQLitePath: *sqlitePath, HealthPath: *healthPath,
	}); err != nil {
		log.Fatalf("seed failed: %v", err)
	}
	log.Printf("seed OK")
}
