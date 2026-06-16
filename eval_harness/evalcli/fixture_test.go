package evalcli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RuntianLee/schema-driven-insight-agent/schema_protocol"
	"gopkg.in/yaml.v3"

	_ "modernc.org/sqlite"
)

const fxSchema = `
version: 1
domain: t
state_tables:
  player_basics:
    nature: snapshot
    primary_key: [player_id]
    fields:
      player_id:        {type: int64, role: actor_id, pk: true, pii: true}
      server_id:        {type: int32, role: dimension}
      level:            {type: int32, role: level}
      last_online_time: {type: unix_timestamp_seconds, role: last_seen}
`

const fxSchemaWithDerived = `
version: 1
domain: t
state_tables:
  player_basics:
    nature: snapshot
    primary_key: [player_id]
    fields:
      player_id:     {type: int64, role: actor_id, pk: true, pii: true}
      server_id:     {type: int32, role: dimension}
      basic_money:   {type: int64, role: balance, currency_type: basic}
      virtual_money: {type: int64, role: balance, currency_type: virtual}
derived_tables:
  player_currencies:
    derived_from: player_basics
    method: pivot_money_columns
    schema:
      player_id:     {type: int64, role: actor_id}
      currency_type: {type: string, role: currency_kind}
      balance:       {type: int64, role: balance}
glossary:
  currency_types:
    basic: "基础货币"
    virtual: "虚拟货币"
`

func parseFixtureNode(t *testing.T, y string) yaml.Node {
	t.Helper()
	var wrapper struct {
		Fixture yaml.Node `yaml:"fixture"`
	}
	if err := yaml.Unmarshal([]byte(y), &wrapper); err != nil {
		t.Fatal(err)
	}
	return wrapper.Fixture
}

func TestBuildFixtureDB_Groups(t *testing.T) {
	s, err := schema_protocol.Parse([]byte(fxSchema))
	if err != nil {
		t.Fatal(err)
	}
	node := parseFixtureNode(t, `
fixture:
  tables:
    player_basics:
      groups:
        - {count: 120, values: {server_id: 1, level: 50, last_online_time: 1716000000}}
        - {count: 60,  values: {server_id: 2, level: 20, last_online_time: 1716000000}}
`)
	db, err := buildFixtureDB(s, node, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM player_basics`).Scan(&n)
	if n != 180 {
		t.Errorf("总行数 %d", n)
	}
	db.QueryRow(`SELECT COUNT(*) FROM player_basics WHERE level = 50`).Scan(&n)
	if n != 120 {
		t.Errorf("group 1 行数 %d", n)
	}
	// 物化列全集建表（player_id 是 PII → 不存在）
	if err := db.QueryRow(`SELECT player_id FROM player_basics LIMIT 1`).Scan(new(any)); err == nil {
		t.Error("PII 列不应存在于 fixture 表")
	}
}

func TestBuildFixtureDB_MaterializesPivotDerivedTable(t *testing.T) {
	s, err := schema_protocol.Parse([]byte(fxSchemaWithDerived))
	if err != nil {
		t.Fatal(err)
	}
	node := parseFixtureNode(t, `
fixture:
  tables:
    player_basics:
      groups:
        - {count: 2, values: {server_id: 1, basic_money: 10, virtual_money: 99}}
        - {count: 1, values: {server_id: 2, basic_money: 20, virtual_money: 199}}
`)
	db, err := buildFixtureDB(s, node, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM player_currencies`).Scan(&n); err != nil {
		t.Fatalf("derived table player_currencies should be queryable: %v", err)
	}
	if n != 6 {
		t.Fatalf("derived row count = %d, want 6", n)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM player_currencies WHERE currency_type = 'virtual' AND balance = 99`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("virtual money rows with balance=99 = %d, want 2", n)
	}
	if err := db.QueryRow(`SELECT COUNT(DISTINCT player_id) FROM player_currencies`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("distinct synthetic player_id = %d, want 3 source rows", n)
	}
}

func TestBuildFixtureDB_RejectsPIIAndUnknown(t *testing.T) {
	s, _ := schema_protocol.Parse([]byte(fxSchema))
	for name, y := range map[string]string{
		"PII 列": `
fixture:
  tables:
    player_basics:
      groups:
        - {count: 1, values: {player_id: 7}}
`,
		"未知列": `
fixture:
  tables:
    player_basics:
      groups:
        - {count: 1, values: {nope: 1}}
`,
		"未知表": `
fixture:
  tables:
    ghost:
      groups:
        - {count: 1, values: {level: 1}}
`,
	} {
		t.Run(name, func(t *testing.T) {
			node := parseFixtureNode(t, y)
			if _, err := buildFixtureDB(s, node, t.TempDir()); err == nil {
				t.Error("应报错拒跑")
			}
		})
	}
}

func TestBuildFixtureDB_EmptyCountRejected(t *testing.T) {
	s, _ := schema_protocol.Parse([]byte(fxSchema))
	node := parseFixtureNode(t, `
fixture:
  tables:
    player_basics:
      groups:
        - {count: 0, values: {level: 1}}
`)
	if _, err := buildFixtureDB(s, node, t.TempDir()); err == nil ||
		!strings.Contains(err.Error(), "count") {
		t.Error("count<=0 应报错")
	}
}

func TestRun_YAMLFixtureEndToEnd(t *testing.T) {
	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "schema.yaml")
	os.WriteFile(schemaPath, []byte(fxSchema), 0o644)
	tasksDir := filepath.Join(dir, "tasks")
	os.MkdirAll(tasksDir, 0o755)
	os.WriteFile(filepath.Join(tasksDir, "t1.yaml"), []byte(`
id: t1
title: "level 分布"
question: "等级分布？"
llm_turns:
  - '{"tool":"query_distribution","args":{"table":"player_basics","column":"level"}}'
  - "等级集中在 50。"
fixture:
  tables:
    player_basics:
      groups:
        - {count: 120, values: {server_id: 1, level: 50, last_online_time: 1716000000}}
evaluators:
  data_correctness:
    tool: query_distribution
    expect_status: OK
    profile: {count: 120}
    rows:
      - match: {bucket: "50"}
        expect: {player_count: 120}
`), 0o644)

	rep, err := Run(Options{Adapter: "t", SchemaPath: schemaPath, TasksDir: tasksDir})
	if err != nil {
		t.Fatal(err)
	}
	if rep.GateFailed() {
		t.Error("YAML fixture 任务 gate 应通过")
	}
}

// TestRun_SharedDBPathMissing：-db 指向不存在路径时，应在 sql.Open 前 Stat 预检并
// 友好报错（防 sqlite 惰性建空库把「路径打错」伪装成「gate 失败」）。
func TestRun_SharedDBPathMissing(t *testing.T) {
	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "schema.yaml")
	os.WriteFile(schemaPath, []byte(fxSchema), 0o644)
	tasksDir := filepath.Join(dir, "tasks")
	os.MkdirAll(tasksDir, 0o755)
	// 任务无 YAML/Go fixture → 走 SharedDBPath 兜底分支。
	os.WriteFile(filepath.Join(tasksDir, "t1.yaml"), []byte(`
id: t1
title: "level 分布"
question: "等级分布？"
llm_turns:
  - '{"tool":"query_distribution","args":{"table":"player_basics","column":"level"}}'
  - "等级集中在 50。"
evaluators:
  data_correctness:
    tool: query_distribution
    expect_status: OK
    profile: {count: 1}
    rows: []
`), 0o644)

	missing := filepath.Join(dir, "does-not-exist.db")
	_, err := Run(Options{Adapter: "t", SchemaPath: schemaPath, TasksDir: tasksDir, SharedDBPath: missing})
	if err == nil {
		t.Fatal("共享 db 路径不存在时应报错")
	}
	if !strings.Contains(err.Error(), "不可用") && !strings.Contains(err.Error(), missing) {
		t.Errorf("报错应含「不可用」或路径，实际: %v", err)
	}
}
