// Command seed 生成 toygame 合成 Layer2 + etl_health.json（开箱即跑）。
package main

import (
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/RuntianLee/schema-driven-insight-agent/etl_health"
	"github.com/RuntianLee/schema-driven-insight-agent/examples/toygame/seed"
)

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func main() {
	dbPath := envOr("SQLITE_PATH", "./data/toygame.db")
	schemaPath := envOr("SCHEMA_PATH", "./schema.yaml")
	healthPath := envOr("ETL_HEALTH_PATH", "./data/etl_health.json")

	for _, dir := range []string{filepath.Dir(dbPath), filepath.Dir(healthPath)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	n, err := seed.Seed(dbPath, schemaPath)
	if err != nil {
		log.Fatalf("seed: %v", err)
	}
	// toygame 是小合成快照：MinRowsOverride 把 agent 启动 Ready() 的行数下限
	// 降到示例量级（否则默认 10w 阈值会让 cmd/agent 对 toygame 直接拒跑）。
	minRows := int64(100)
	if err := etl_health.Write(healthPath, etl_health.Health{
		Status:          etl_health.StatusOK,
		Rows:            n,
		FinishedAt:      time.Now(),
		SchemaVersion:   1,
		Frozen:          true, // 合成快照，跳失鲜门
		MinRowsOverride: &minRows,
	}); err != nil {
		log.Fatalf("health: %v", err)
	}
	log.Printf("toygame seed OK: %d players → %s | health → %s", n, dbPath, healthPath)
}
