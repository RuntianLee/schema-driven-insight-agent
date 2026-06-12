package schema_protocol

import (
	"strings"
	"testing"
)

const policySchema = `
version: 1
domain: t
etl_policy:
  hash_salt: t_v0
  min_rows: 5000
  health_min_rows: 9000
  frozen: true
  health_path: ./data/etl_health_t.json
state_tables:
  player_basics:
    nature: snapshot
    primary_key: [player_id]
    fields:
      player_id: {type: int64, role: actor_id, pk: true, pii: true}
      server_id: {type: int32, role: dimension, index: true}
      coins:     {type: int64, role: balance, currency_type: coins}
      last_seen: {type: unix_timestamp_seconds, role: last_seen}
derived_tables:
  player_currencies:
    derived_from: player_basics
    method: pivot_money_columns
    schema:
      player_id:     {type: int64,  role: actor_id}
      currency_type: {type: string, role: currency_kind}
      balance:       {type: int64,  role: balance}
`

func TestParse_ETLPolicyAndIndex(t *testing.T) {
	s, err := Parse([]byte(policySchema))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	p := s.ETLPolicy
	if p == nil {
		t.Fatal("ETLPolicy 应解析为非 nil")
	}
	if p.HashSalt != "t_v0" || p.MinRows != 5000 || p.HealthMinRows != 9000 ||
		!p.Frozen || p.HealthPath != "./data/etl_health_t.json" {
		t.Errorf("etl_policy 字段解析错误: %+v", p)
	}
	if !s.StateTables["player_basics"].Fields["server_id"].Index {
		t.Error("index: true 未解析")
	}
}

func TestParse_NoPolicyBackCompat(t *testing.T) {
	// 旧 schema（无 etl_policy）必须照常解析，ETLPolicy 为 nil。
	s, err := Parse([]byte(strings.Join([]string{
		"version: 1", "domain: t",
		"state_tables:", "  t1:", "    fields:",
		"      a: {type: int64, role: level}",
	}, "\n")))
	if err != nil {
		t.Fatalf("旧 schema 解析失败: %v", err)
	}
	if s.ETLPolicy != nil {
		t.Error("未声明 etl_policy 时应为 nil")
	}
}

func TestParse_PolicyValidation(t *testing.T) {
	cases := []struct{ name, find, replace, wantErr string }{
		{"min_rows 必填", "min_rows: 5000", "min_rows: 0", "min_rows"},
		{"派生表须有盐", "hash_salt: t_v0", "hash_salt: ''", "hash_salt"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			y := strings.Replace(policySchema, c.find, c.replace, 1)
			if _, err := Parse([]byte(y)); err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("want error containing %q, got %v", c.wantErr, err)
			}
		})
	}
}

func TestParse_TODORejected(t *testing.T) {
	roleTODO := strings.Replace(policySchema,
		"server_id: {type: int32, role: dimension, index: true}",
		"server_id: {type: int32, role: TODO, index: true}", 1)
	if _, err := Parse([]byte(roleTODO)); err == nil || !strings.Contains(err.Error(), "TODO") {
		t.Errorf("role: TODO 应拒绝解析, got %v", err)
	}
	piiTODO := strings.Replace(policySchema,
		"player_id: {type: int64, role: actor_id, pk: true, pii: true}",
		"player_id: {type: int64, role: actor_id, pk: true, pii: TODO}", 1)
	if _, err := Parse([]byte(piiTODO)); err == nil || !strings.Contains(err.Error(), "pii") {
		t.Errorf("pii: TODO 应拒绝解析, got %v", err)
	}
}

func TestParse_IndexOnUnmaterializedRejected(t *testing.T) {
	y := strings.Replace(policySchema,
		"player_id: {type: int64, role: actor_id, pk: true, pii: true}",
		"player_id: {type: int64, role: actor_id, pk: true, pii: true, index: true}", 1)
	if _, err := Parse([]byte(y)); err == nil || !strings.Contains(err.Error(), "index") {
		t.Errorf("PII 列标 index 应拒绝, got %v", err)
	}
}
