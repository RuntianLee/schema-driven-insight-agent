package etl

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/RuntianLee/schema-driven-insight-agent/schema_protocol"
)

// tdLikeSchema 镜像 td adapter 现行 schema 的关键面，断言 buildPlan 装配结果
// 与迁移前手写常量（adapters/td/etl/etl.go）逐项一致——推导语义对账测试。
const tdLikeSchema = `
version: 1
domain: game_operation
etl_policy:
  hash_salt: td_player_v0
  min_rows: 5000
  health_min_rows: 5000
  frozen: true
  health_path: ./data/etl_health_td.json
data_sources:
  game_db: {type: postgres, dsn_env: TD_PG_DSN, access: read_only}
  layer2:  {type: sqlite, path: ./data/td.db}
state_tables:
  player_basics:
    nature: snapshot
    primary_key: [player_id]
    fields:
      player_id:            {type: int64, role: actor_id, pk: true, pii: true}
      server_id:            {type: int32, role: dimension, index: true}
      level:                {type: int32, role: level, index: true}
      basic_money:          {type: int64, role: balance, currency_type: basic}
      premium_money:        {type: int64, role: balance, currency_type: premium}
      virtual_money:        {type: int32, role: balance, currency_type: virtual}
      passed_main_stage_id: {type: int32, role: stage_progress, index: true}
      last_online_time:     {type: unix_timestamp_seconds, role: last_seen, index: true}
derived_tables:
  player_currencies:
    derived_from: player_basics
    method: pivot_money_columns
    schema:
      player_id:     {type: int64,  role: actor_id}
      currency_type: {type: string, role: currency_kind}
      balance:       {type: int64,  role: balance}
`

func TestBuildPlan_TDParity(t *testing.T) {
	s, err := schema_protocol.Parse([]byte(tdLikeSchema))
	if err != nil {
		t.Fatal(err)
	}
	o := RunOptions{SchemaPath: "/x/adapters/td/schema.yaml", DSN: "dsn"}
	plan, err := buildPlan(s, o)
	if err != nil {
		t.Fatal(err)
	}

	// basics：与 td etl.go PullPlayerBasics 的手写值一致
	if len(plan.Basics) != 1 {
		t.Fatalf("basics 数量 %d", len(plan.Basics))
	}
	b := plan.Basics[0]
	if b.Table != "player_basics" || b.MinRows != 5000 {
		t.Errorf("basics 装配: %+v", b)
	}
	wantIdx := []string{"last_online_time", "level", "passed_main_stage_id", "server_id"}
	if !reflect.DeepEqual(b.IndexCols, wantIdx) {
		t.Errorf("索引列 got %v want %v（td 手写列表的字母序）", b.IndexCols, wantIdx)
	}
	if b.SQLitePath != filepath.Join("/x/adapters/td", "data/td.db") {
		t.Errorf("sqlite 路径应取 schema data_sources.layer2.path 相对 schema 目录: %s", b.SQLitePath)
	}

	// stamps：last_seen 列
	if len(plan.Stamps) != 1 || plan.Stamps[0].Column != "last_online_time" {
		t.Errorf("stamps: %+v", plan.Stamps)
	}

	// currencies：与 td etl.go PullPlayerCurrencies 的手写值一致
	c := plan.Currencies
	wantMoney := []MoneyColumn{
		{Column: "basic_money", CurrencyType: "basic"},
		{Column: "premium_money", CurrencyType: "premium"},
		{Column: "virtual_money", CurrencyType: "virtual"},
	}
	if !reflect.DeepEqual(c.Money, wantMoney) {
		t.Errorf("货币列 got %+v", c.Money)
	}
	if c.SrcTable != "player_basics" || c.DestTable != "player_currencies" ||
		c.PIDCol != "player_id" || c.Salt != "td_player_v0" ||
		c.MinRows != 5000 || c.SchemaVersion != 1 || !c.Frozen {
		t.Errorf("currencies 装配: %+v", c)
	}
	if c.MinRowsOverride == nil || *c.MinRowsOverride != 5000 {
		t.Errorf("health_min_rows → MinRowsOverride: %v", c.MinRowsOverride)
	}
	if c.HealthPath != filepath.Join("/x/adapters/td", "data/etl_health_td.json") {
		t.Errorf("health 路径应取 etl_policy.health_path 相对 schema 目录: %s", c.HealthPath)
	}
}

func TestBuildPlan_Errors(t *testing.T) {
	noPolicy, err := schema_protocol.Parse([]byte("version: 1\ndomain: t\nstate_tables:\n  t1:\n    fields:\n      a: {type: int64, role: level}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := buildPlan(noPolicy, RunOptions{SchemaPath: "x.yaml"}); err == nil {
		t.Error("缺 etl_policy 应报错")
	}
}

func replaceOnce(t *testing.T, s, oldS, newS string) string {
	t.Helper()
	out := strings.Replace(s, oldS, newS, 1)
	if out == s {
		t.Fatalf("replace 未命中: %q", oldS)
	}
	return out
}

func TestBuildPlan_SaltEnv(t *testing.T) {
	y := replaceOnce(t, tdLikeSchema, "hash_salt: td_player_v0", "hash_salt_env: TD_SALT")
	s, err := schema_protocol.Parse([]byte(y))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TD_SALT", "from_env")
	plan, err := buildPlan(s, RunOptions{SchemaPath: "x.yaml", DSN: "d"})
	if err != nil || plan.Currencies.Salt != "from_env" {
		t.Errorf("salt env 解析: %v", err)
	}
	t.Setenv("TD_SALT", "")
	if _, err := buildPlan(s, RunOptions{SchemaPath: "x.yaml", DSN: "d"}); err == nil {
		t.Error("salt env 为空应报错")
	}
}

func TestDSNEnvFromSchema(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "schema.yaml")
	os.WriteFile(p, []byte(tdLikeSchema), 0o644)
	env, err := DSNEnvFromSchema(p)
	if err != nil || env != "TD_PG_DSN" {
		t.Errorf("got %q, %v", env, err)
	}
}
