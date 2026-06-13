package schema_protocol

import (
	"testing"
)

const validYAML = `
version: 2
domain: game_operation
data_sources:
  layer2: {type: sqlite, path: ./data/test.db}
derived_tables:
  player_currencies:
    derived_from: player_basics
    method: pivot_money_columns
    schema:
      player_id:     {type: int64, role: actor_id}
      currency_type: {type: string, role: currency_kind, glossary_key: currency_types}
      balance:       {type: int64, role: balance}
glossary:
  currency_types:
    coins: "游戏货币"
  buckets:
    currency_balance:
      - {min: 0,      max: 10000,  label: "0~1w"}
      - {min: 10001,  max: 100000, label: "1~10w"}
      - {min: 100001, max: null,   label: "10w+"}
`

func TestParseValid(t *testing.T) {
	s, err := Parse([]byte(validYAML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, ok := s.DerivedTables["player_currencies"]; !ok {
		t.Fatal("missing player_currencies")
	}
	b := s.Glossary.Buckets["currency_balance"]
	if len(b) != 3 || b[2].Max != 0 {
		t.Fatalf("buckets wrong: %+v", b)
	}
}

func TestResolveColumnUndefined(t *testing.T) {
	s, _ := Parse([]byte(validYAML))
	if _, err := s.ResolveColumn("player_currencies", "no_such_col"); err == nil {
		t.Fatal("expected error for undefined column")
	}
	if _, err := s.ResolveColumn("no_such_table", "balance"); err == nil {
		t.Fatal("expected error for undefined table")
	}
	if _, err := s.ResolveColumn("player_currencies", "balance"); err != nil {
		t.Fatalf("balance should resolve: %v", err)
	}
}

func TestParseBucketOutOfOrder(t *testing.T) {
	bad := `
version: 1
domain: x
glossary:
  buckets:
    b:
      - {min: 0,     max: 100, label: "a"}
      - {min: 50,    max: 200, label: "b"}
`
	if _, err := Parse([]byte(bad)); err == nil {
		t.Fatal("expected overlap error")
	}
}

func TestParseInlineSchema(t *testing.T) {
	// 验证内联 testSchemaYAML（同 package）可被 framework 正确解析。
	s, err := Parse([]byte(testSchemaYAML))
	if err != nil {
		t.Fatalf("parse inline schema: %v", err)
	}
	if _, err := s.ResolveColumn("player_currencies", "balance"); err != nil {
		t.Fatalf("balance must resolve: %v", err)
	}
	if len(s.Glossary.Buckets["coins_balance"]) != 5 {
		t.Fatal("expected 5 coins_balance buckets")
	}
}

func TestParseTuning(t *testing.T) {
	yaml := `
version: 1
domain: test
tuning:
  rows_attach_threshold: 500
  value_top_n: 5
  groups_top_n: 8
  per_group_rows_attach_threshold: 33
state_tables:
  t1:
    nature: snapshot
    primary_key: [id]
    fields:
      id: {type: int64, role: actor_id, pii: true}
`
	s, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if s.Tuning.RowsAttachThreshold != 500 {
		t.Fatalf("RowsAttachThreshold = %d, want 500", s.Tuning.RowsAttachThreshold)
	}
	if s.Tuning.ValueTopN != 5 {
		t.Fatalf("ValueTopN = %d, want 5", s.Tuning.ValueTopN)
	}
	if s.Tuning.GroupsTopN != 8 {
		t.Fatalf("GroupsTopN = %d, want 8", s.Tuning.GroupsTopN)
	}
	if s.Tuning.PerGroupRowsAttachThreshold != 33 {
		t.Fatalf("PerGroupRowsAttachThreshold = %d, want 33", s.Tuning.PerGroupRowsAttachThreshold)
	}
}

func TestParseTuning_AbsentLeavesZeros(t *testing.T) {
	// 用内联最小 yaml 验证 absent 行为（不依赖 adapter schema）。
	yaml := `
version: 1
domain: test
state_tables:
  t1:
    nature: snapshot
    primary_key: [id]
    fields:
      id: {type: int64, role: actor_id, pii: true}
`
	s, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if s.Tuning != (Tuning{}) {
		t.Fatalf("absent tuning: %+v, want zero value", s.Tuning)
	}
}

func TestParse_ETLPolicyDataAsOf(t *testing.T) {
	y := []byte(`
version: 1
domain: bank_churn
etl_policy:
  hash_salt: s
  min_rows: 1
  data_as_of: 1704067200
state_tables:
  customers:
    fields:
      id: {type: int64, role: actor_id, pk: true}
`)
	s, err := Parse(y)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if s.ETLPolicy == nil || s.ETLPolicy.DataAsOf != 1704067200 {
		t.Fatalf("DataAsOf = %v, want 1704067200", s.ETLPolicy)
	}
}
