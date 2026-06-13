package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/RuntianLee/schema-driven-insight-agent/etl/csvload"
)

func TestCmdCSV_Smoke(t *testing.T) {
	dir := t.TempDir()
	schema := `
version: 1
domain: d
etl_policy: {hash_salt: s, min_rows: 1, frozen: true, data_as_of: 1700000000}
data_sources:
  source: {type: csv, path: ./x.csv}
  layer2: {type: sqlite, path: ./x.db}
state_tables:
  t:
    fields:
      Id: {type: int64, role: actor_id, pk: true, pii: true}
      M:  {type: int64, role: balance, currency_type: c}
derived_tables:
  cb: {derived_from: t, method: pivot_money_columns, schema: {player_id: {type: int64, role: actor_id}, currency_type: {type: string, role: currency_kind}, balance: {type: int64, role: balance}}}
`
	os.WriteFile(filepath.Join(dir, "schema.yaml"), []byte(schema), 0o644)
	os.WriteFile(filepath.Join(dir, "x.csv"), []byte("Id,M\n1,100\n2,200\n"), 0o644)
	if err := csvload.Run(csvload.RunOptions{SchemaPath: filepath.Join(dir, "schema.yaml")}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "x.db")); err != nil {
		t.Fatalf("db 未生成: %v", err)
	}
}
