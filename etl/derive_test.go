package etl

import (
	"reflect"
	"testing"

	"github.com/RuntianLee/schema-driven-insight-agent/schema_protocol"
)

const deriveSchema = `
version: 1
domain: t
state_tables:
  player_basics:
    nature: snapshot
    primary_key: [player_id]
    fields:
      player_id:     {type: int64, role: actor_id, pk: true, pii: true}
      server_id:     {type: int32, role: dimension, index: true}
      virtual_money: {type: int32, role: balance, currency_type: virtual}
      basic_money:   {type: int64, role: balance, currency_type: basic}
      level:         {type: int32, role: level, index: true}
      secret_col:    {type: int64, role: dimension, omit_in_layer2: true}
      last_online:   {type: unix_timestamp_seconds, role: last_seen}
`

func mustParse(t *testing.T) *schema_protocol.Schema {
	t.Helper()
	s, err := schema_protocol.Parse([]byte(deriveSchema))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestMoneyColumnsFor(t *testing.T) {
	got, err := MoneyColumnsFor(mustParse(t), "player_basics")
	if err != nil {
		t.Fatal(err)
	}
	want := []MoneyColumn{ // 列名字母序，确定性
		{Column: "basic_money", CurrencyType: "basic"},
		{Column: "virtual_money", CurrencyType: "virtual"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v want %+v", got, want)
	}
	if _, err := MoneyColumnsFor(mustParse(t), "nope"); err == nil {
		t.Error("未知表应报错")
	}
}

func TestIndexColumnsFor(t *testing.T) {
	got := IndexColumnsFor(mustParse(t), "player_basics")
	want := []string{"level", "server_id"} // 字母序
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

// TestIndexColumnsFor_FiltersPIIAndOmit 验证 derive 层的「防御纵深」：即便某列被
// 同时标了 index+pii（parser 的 validateIndexFlags 本会拒绝这种 schema，故无法走
// Parse 构造），IndexColumnsFor 仍独立把它过滤掉。用内存构造 Schema 绕过 parser
// 校验，正面证明 derive 过滤逻辑不依赖上游 parser 才成立——纵深防御。
func TestIndexColumnsFor_FiltersPIIAndOmit(t *testing.T) {
	s := &schema_protocol.Schema{
		StateTables: map[string]schema_protocol.Table{
			"player_basics": {
				Nature:     "snapshot",
				PrimaryKey: []string{"player_id"},
				Fields: map[string]schema_protocol.FieldDef{
					"server_id":    {Type: "int32", Role: "dimension", Index: true},
					"pii_indexed":  {Type: "int64", Role: "dimension", Index: true, PII: true},          // PII → 过滤
					"omit_indexed": {Type: "int64", Role: "dimension", Index: true, OmitInLayer2: true}, // 未物化 → 过滤
				},
			},
		},
	}
	got := IndexColumnsFor(s, "player_basics")
	want := []string{"server_id"} // 仅保留物化、非 PII 的索引列
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v (PII/omit 列应被过滤)", got, want)
	}
}

func TestActorIDColumn(t *testing.T) {
	col, err := ActorIDColumn(mustParse(t), "player_basics")
	if err != nil || col != "player_id" {
		t.Errorf("got %q, %v", col, err)
	}
}

// TestActorIDColumn_WrongCount 补 0 个与 2 个 actor_id 的报错分支（内存构造，
// 因为 0 个 actor_id 的合法 schema 难以经由真实 parser 触发该 derive 分支）。
func TestActorIDColumn_WrongCount(t *testing.T) {
	zero := &schema_protocol.Schema{
		StateTables: map[string]schema_protocol.Table{
			"t": {Fields: map[string]schema_protocol.FieldDef{
				"a": {Type: "int64", Role: "dimension"},
			}},
		},
	}
	if _, err := ActorIDColumn(zero, "t"); err == nil {
		t.Error("0 个 actor_id 应报错")
	}

	two := &schema_protocol.Schema{
		StateTables: map[string]schema_protocol.Table{
			"t": {Fields: map[string]schema_protocol.FieldDef{
				"a": {Type: "int64", Role: "actor_id"},
				"b": {Type: "int64", Role: "actor_id"},
			}},
		},
	}
	if _, err := ActorIDColumn(two, "t"); err == nil {
		t.Error("2 个 actor_id 应报错")
	}
}

func TestLastSeenColumn(t *testing.T) {
	col, ok := LastSeenColumn(mustParse(t), "player_basics")
	if !ok || col != "last_online" {
		t.Errorf("got %q, %v", col, ok)
	}
}

// TestLastSeenColumn_PIIRejected 补「PII last_seen → ok=false」分支：last_seen 列
// 若被标 PII（不物化暴露），不能作为 data_as_of 锚点。内存构造绕过 parser。
func TestLastSeenColumn_PIIRejected(t *testing.T) {
	s := &schema_protocol.Schema{
		StateTables: map[string]schema_protocol.Table{
			"t": {Fields: map[string]schema_protocol.FieldDef{
				"last_online": {Type: "unix_timestamp_seconds", Role: "last_seen", PII: true},
			}},
		},
	}
	if _, ok := LastSeenColumn(s, "t"); ok {
		t.Error("PII last_seen 列不应作为锚点，应 ok=false")
	}
}
