package schema_protocol

import (
	"strings"
	"testing"
)

func TestDigest_InlineSchema(t *testing.T) {
	s, err := Parse([]byte(testSchemaYAML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	d := s.Digest()

	// ── 必须包含 wedge 核心名字（derived table + 字段 + 词汇表）──
	for _, want := range []string{
		"player_currencies",
		"balance",
		"currency_type",
		"coins_balance",
		"coins",
		"player_basics (state)", // 段标记锁定（state 表确实被渲染）
		"player_currencies (derived)",
		"level",
		"quest_level",
		"server_id",
		"last_online_time",
	} {
		if !strings.Contains(d, want) {
			t.Errorf("Digest() missing %q:\n%s", want, d)
		}
	}

	// ── 不得含任何 bucket boundary 数值 ──
	for _, forbidden := range []string{
		// bucket boundary values from inline schema
		"10000", "10001", "100000", "100001", "200000", "200001", "500000", "500001",
	} {
		if strings.Contains(d, forbidden) {
			t.Errorf("Digest() must NOT contain numeric boundary value %q:\n%s", forbidden, d)
		}
	}

	// ── PII 列：state 段不暴露，derived 段的 player_id 属预期（恰好出现一次）──
	// player_basics.player_id 是 pii → state 段必须排除；derived player_currencies 的
	// player_id 属预期 → 全文恰好出现一次（锁定 state 段未泄露原始 player_id）。
	if n := strings.Count(d, "player_id"); n != 1 {
		t.Errorf("player_id should appear exactly once (derived only), got %d:\n%s", n, d)
	}

	// ── 确定性：两次调用结果相同 ──
	d2 := s.Digest()
	if d != d2 {
		t.Errorf("Digest() is not deterministic:\nfirst =%s\nsecond=%s", d, d2)
	}
}

// TestDigest_StateTablePIIFilter 用内存 schema 隔离覆盖 state 表的 PII 过滤分支
// （不依赖磁盘上的 adapter schema）：非-PII 列暴露，pii / omit_in_layer2 列恒不暴露。
func TestDigest_StateTablePIIFilter(t *testing.T) {
	const yamlSrc = `
version: 1
domain: test
state_tables:
  t1:
    nature: snapshot
    primary_key: [id]
    fields:
      id:     {type: int64, role: actor_id, pii: true}
      secret: {type: string, role: actor_name, omit_in_layer2: true}
      lvl:    {type: int16, role: level}
glossary:
  buckets:
    lvl_b:
      - {min: 0,  max: 10,   label: "low"}
      - {min: 11, max: null, label: "high"}
`
	s, err := Parse([]byte(yamlSrc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	d := s.Digest()
	if !strings.Contains(d, "lvl(role=level)") {
		t.Errorf("non-PII column lvl must be exposed:\n%s", d)
	}
	for _, forbidden := range []string{"id(role=actor_id)", "secret(role=actor_name)"} {
		if strings.Contains(d, forbidden) {
			t.Errorf("PII/omit column %q must NOT be exposed:\n%s", forbidden, d)
		}
	}
}

func TestDigest_Minimal(t *testing.T) {
	// 用 validYAML（已在 parser_test.go 定义，同 package）测试基本结构
	s, err := Parse([]byte(validYAML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	d := s.Digest()

	// 必须含 derived table
	if !strings.Contains(d, "player_currencies") {
		t.Errorf("missing player_currencies:\n%s", d)
	}
	// 必须含 bucket key 节
	if !strings.Contains(d, "currency_balance") {
		t.Errorf("missing currency_balance:\n%s", d)
	}
	// 必须含 currency_type 节
	if !strings.Contains(d, "coins") {
		t.Errorf("missing coins currency type:\n%s", d)
	}
	// bucket boundary values must not appear
	for _, num := range []string{"10000", "10001", "100000", "100001"} {
		if strings.Contains(d, num) {
			t.Errorf("Digest() must NOT contain boundary %q:\n%s", num, d)
		}
	}
}
