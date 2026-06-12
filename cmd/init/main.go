// Command init 内省生产 PG，生成零代码接入包草稿（schema.yaml + db config +
// 任务/seed 骨架 + .gitignore）。草稿含 role/pii TODO 占位，解析器拒绝 TODO——
// 必须人工完成标注才可运行（杜绝「忘标 PII 就物化」）。
//
// 用法：
//
//	go run github.com/RuntianLee/schema-driven-insight-agent/cmd/init \
//	  -db-config ./config/db.yaml -tables player_basics -domain my_game -out ./my-adapter
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/RuntianLee/schema-driven-insight-agent/etl"
	"github.com/RuntianLee/schema-driven-insight-agent/etl/introspect"
)

func main() {
	dbConfig := flag.String("db-config", "./config/db.yaml", "PG YAML config（或设 -dsn-env 指定的环境变量）")
	dsnEnv := flag.String("dsn-env", "PG_DSN", "DSN 环境变量名（config 缺失时兜底；也写入草稿 data_sources）")
	tablesCSV := flag.String("tables", "", "要接入的表名（逗号分隔，必填）")
	domain := flag.String("domain", "my_domain", "业务域名（写入草稿 domain + 路径）")
	outDir := flag.String("out", "./my-adapter", "草稿输出目录")
	flag.Parse()

	if *tablesCSV == "" {
		log.Fatal("用 -tables 指定要接入的表（逗号分隔），如 -tables player_basics")
	}
	tables := strings.Split(*tablesCSV, ",")
	for i := range tables {
		tables[i] = strings.TrimSpace(tables[i])
	}

	// SECURITY: dsn 绝不打印。
	dsn, summary, err := etl.ResolveDSNFromConfig(*dbConfig, *dsnEnv)
	if err != nil {
		log.Fatalf("PG 未配置: %v", err)
	}
	log.Printf("PG: %s", summary)

	infos, err := introspect.Introspect(context.Background(), dsn, tables)
	if err != nil {
		log.Fatalf("introspect: %v", err)
	}

	schemaPath := filepath.Join(*outDir, "schema.yaml")
	if _, err := os.Stat(schemaPath); err == nil {
		log.Fatalf("%s 已存在，拒绝覆盖（删除或换 -out 目录）", schemaPath)
	}
	if err := os.MkdirAll(filepath.Join(*outDir, "eval/tasks"), 0o755); err != nil {
		log.Fatal(err)
	}
	writes := []struct {
		path string
		data []byte
	}{
		{schemaPath, introspect.RenderSchema(*domain, *dsnEnv, infos)},
		{filepath.Join(*outDir, "db-config.example.yaml"), introspect.RenderDBConfigExample()},
		{filepath.Join(*outDir, "eval/tasks/example_task.yaml"), introspect.RenderTaskSkeleton()},
		{filepath.Join(*outDir, "seed.example.yaml"), introspect.RenderSeedExample(infos)},
		{filepath.Join(*outDir, ".gitignore"), introspect.RenderGitignore()},
	}
	for _, w := range writes {
		if err := os.WriteFile(w.path, w.data, 0o644); err != nil {
			log.Fatalf("write %s: %v", w.path, err)
		}
		log.Printf("生成 %s", w.path)
	}
	log.Printf("草稿就绪。下一步：完成 schema.yaml 全部 TODO（role/pii 标注）→ cmd/etl 或 cmd/seed → cmd/agent / cmd/eval")
}
