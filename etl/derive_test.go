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

func TestActorIDColumn(t *testing.T) {
	col, err := ActorIDColumn(mustParse(t), "player_basics")
	if err != nil || col != "player_id" {
		t.Errorf("got %q, %v", col, err)
	}
}

func TestLastSeenColumn(t *testing.T) {
	col, ok := LastSeenColumn(mustParse(t), "player_basics")
	if !ok || col != "last_online" {
		t.Errorf("got %q, %v", col, ok)
	}
}
