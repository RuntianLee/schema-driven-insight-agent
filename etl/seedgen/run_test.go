package seedgen

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RuntianLee/schema-driven-insight-agent/etl_health"

	_ "modernc.org/sqlite"
)

const runSchema = `
version: 1
domain: toy
etl_policy:
  hash_salt: toy_v0
  min_rows: 1
  health_min_rows: 100
  frozen: true
data_sources:
  layer2: {type: sqlite, path: ./data/toy.db}
state_tables:
  player_basics:
    nature: snapshot
    primary_key: [player_id]
    fields:
      player_id:  {type: int64, role: actor_id, pk: true, pii: true}
      level:      {type: int32, role: level, index: true}
      coins:      {type: int64, role: balance, currency_type: coins}
      last_login: {type: unix_timestamp_seconds, role: last_seen}
derived_tables:
  player_currencies:
    derived_from: player_basics
    method: pivot_money_columns
    schema:
      player_id:     {type: int64,  role: actor_id}
      currency_type: {type: string, role: currency_kind}
      balance:       {type: int64,  role: balance}
`

const runSpec = `
as_of: 1700000000
tables:
  player_basics:
    rows: 1000
    columns:
      coins:
        enum:
          - {value: 50, weight: 600}
          - {value: 500, weight: 300}
          - {value: 5000, weight: 80}
          - {value: 50000, weight: 20}
      level: {buckets: [{min: 1, max: 30}]}
      last_login: {const: 1700000000}
`

func writeTemp(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRun_EndToEnd(t *testing.T) {
	dir := t.TempDir()
	schemaPath := writeTemp(t, dir, "schema.yaml", runSchema)
	specPath := writeTemp(t, dir, "seed.yaml", runSpec)

	if err := Run(RunOptions{SchemaPath: schemaPath, SpecPath: specPath}); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(dir, "data/toy.db") // layer2.path 相对 schema 目录
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var n int
	db.QueryRow(`SELECT COUNT(*) FROM player_basics`).Scan(&n)
	if n != 1000 {
		t.Errorf("basics 行数 %d", n)
	}
	db.QueryRow(`SELECT COUNT(*) FROM player_currencies WHERE balance = 50`).Scan(&n)
	if n != 600 {
		t.Errorf("enum 精确配额经 pivot 保持: %d", n)
	}
	db.QueryRow(`SELECT COUNT(DISTINCT player_id) FROM player_currencies`).Scan(&n)
	if n != 1000 {
		t.Errorf("脱敏 hash 应唯一: %d", n)
	}
	var asOf string
	db.QueryRow(`SELECT value FROM _meta WHERE key='data_as_of'`).Scan(&asOf)
	if asOf != "1700000000" {
		t.Errorf("data_as_of: %s", asOf)
	}

	h, err := etl_health.Read(filepath.Join(dir, "data/etl_health.json"))
	if err != nil {
		t.Fatal(err)
	}
	if h.Status != etl_health.StatusOK || h.Rows != 1000 || !h.Frozen ||
		h.DataAsOf != 1700000000 || h.MinRowsOverride == nil || *h.MinRowsOverride != 100 {
		t.Errorf("health: %+v", h)
	}
}

func TestRun_MissingColumnGeneratorLoudFail(t *testing.T) {
	dir := t.TempDir()
	schemaPath := writeTemp(t, dir, "schema.yaml", runSchema)
	bad := `
tables:
  player_basics:
    rows: 10
    columns:
      coins: {const: 1}
`
	specPath := writeTemp(t, dir, "seed.yaml", bad)
	err := Run(RunOptions{SchemaPath: schemaPath, SpecPath: specPath})
	if err == nil {
		t.Fatal("缺生成器应报错")
	}
	for _, miss := range []string{"last_login", "level"} {
		if !strings.Contains(err.Error(), miss) {
			t.Errorf("错误应列出缺失列 %s: %v", miss, err)
		}
	}
}
