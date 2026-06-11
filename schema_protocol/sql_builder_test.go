package schema_protocol

import (
	"strings"
	"testing"
)

func loadTestSchema(t *testing.T) *Schema {
	t.Helper()
	s, err := Parse([]byte(testSchemaYAML))
	if err != nil {
		t.Fatalf("parse inline schema: %v", err)
	}
	return s
}

func TestBuildDistribution_WedgeUsesPlaceholder(t *testing.T) {
	s := loadTestSchema(t)
	sql, args, err := s.BuildDistribution(DistQuery{
		Table:     "player_currencies",
		Column:    "balance",
		Filter:    map[string]any{"currency_type": "coins"},
		BucketKey: "coins_balance",
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !strings.Contains(sql, "currency_type = ?") {
		t.Fatalf("filter must be parameterized, got: %s", sql)
	}
	if strings.Contains(sql, "'coins'") {
		t.Fatalf("filter value must NOT be inlined: %s", sql)
	}
	if len(args) != 1 || args[0] != "coins" {
		t.Fatalf("args mismatch: %v", args)
	}
	for _, want := range []string{
		"0~1w", "1~10w", "50w+",
		"COUNT(*)", "pct_players", "pct_value", "total_value", "avg_value",
		"cum_pct_players", "cum_pct_value", "WITH agg AS",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("sql missing %q: %s", want, sql)
		}
	}
}

func TestBuildDistribution_GroupByWhitelist(t *testing.T) {
	s := loadTestSchema(t)
	_, _, err := s.BuildDistribution(DistQuery{
		Table:     "player_currencies",
		Column:    "balance",
		BucketKey: "coins_balance",
		GroupBy:   []string{"balance; DROP TABLE player_currencies"},
	})
	if err == nil {
		t.Fatal("group_by injection must be rejected by whitelist")
	}
	if _, ok := err.(*SchemaError); !ok {
		t.Fatalf("expected *SchemaError, got %T", err)
	}
	if err.Error() == "" {
		t.Fatal("SchemaError.Error() must return non-empty string")
	}
}

func TestBuildDistribution_GroupBySingleDim(t *testing.T) {
	s := loadTestSchema(t)
	sql, _, err := s.BuildDistribution(DistQuery{
		Table:     "player_currencies",
		Column:    "balance",
		BucketKey: "coins_balance",
		GroupBy:   []string{"currency_type"},
	})
	if err != nil {
		t.Fatalf("single-dim group_by must succeed: %v", err)
	}
	for _, want := range []string{
		"currency_type AS grp",
		"GROUP BY grp, bucket, ord",
		"PARTITION BY grp",
		"cum_pct_players",
		"PARTITION BY grp ORDER BY ord DESC",
		"ORDER BY grp, ord",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("grouped sql missing %q: %s", want, sql)
		}
	}
}

func TestBuildDistribution_GroupByMultiDimRejected(t *testing.T) {
	s := loadTestSchema(t)
	_, _, err := s.BuildDistribution(DistQuery{
		Table:     "player_currencies",
		Column:    "balance",
		BucketKey: "coins_balance",
		GroupBy:   []string{"currency_type", "balance"},
	})
	if err == nil {
		t.Fatal("multi-dim group_by must be rejected in V1")
	}
	se, ok := err.(*SchemaError)
	if !ok {
		t.Fatalf("expected *SchemaError, got %T", err)
	}
	if se.Path != "group_by" {
		t.Fatalf("Path must be \"group_by\", got %q", se.Path)
	}
}

func TestBuildDistribution_RawValueNoBucket(t *testing.T) {
	s := loadTestSchema(t)
	// quest_level 非 balance + 无 bucket_key → 原始值分布。
	sql, _, err := s.BuildDistribution(DistQuery{
		Table: "player_basics", Column: "quest_level",
	})
	if err != nil {
		t.Fatalf("raw-value build must succeed: %v", err)
	}
	for _, want := range []string{
		"CAST(quest_level AS TEXT) AS bucket",
		"quest_level AS ord",
		"GROUP BY bucket, ord",
		"cum_pct_players",
		"ORDER BY ord",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("raw-value sql missing %q: %s", want, sql)
		}
	}
	// 非 balance 列不得输出价值列。
	for _, forbidden := range []string{"total_value", "pct_value", "avg_value", "cum_pct_value"} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("non-balance distribution must NOT emit %q: %s", forbidden, sql)
		}
	}
}

func TestBuildDistribution_RawValueWithComparisonFilter(t *testing.T) {
	s := loadTestSchema(t)
	// #1 流失分级：filter last_online_time < cutoff + 按 level 原始值分布。
	sql, args, err := s.BuildDistribution(DistQuery{
		Table: "player_basics", Column: "level",
		Filter: map[string]any{"last_online_time": map[string]any{"op": "<", "value": int64(1716800000)}},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !strings.Contains(sql, "last_online_time < ?") {
		t.Fatalf("comparison filter missing: %s", sql)
	}
	if !strings.Contains(sql, "CAST(level AS TEXT) AS bucket") {
		t.Fatalf("raw-value label missing: %s", sql)
	}
	if len(args) != 1 || args[0] != int64(1716800000) {
		t.Fatalf("args mismatch: %v", args)
	}
}

func TestBuildDistribution_RejectsPIIField(t *testing.T) {
	s := loadTestSchema(t)
	// player_basics.player_id 是 pii — 不可查。
	for _, col := range []string{"player_id"} {
		if _, _, err := s.BuildDistribution(DistQuery{
			Table: "player_basics", Column: col, // 原始值模式（无 bucket_key）
		}); err == nil {
			t.Fatalf("PII column %q must be rejected", col)
		} else if _, ok := err.(*SchemaError); !ok {
			t.Fatalf("expected *SchemaError for %q, got %T", col, err)
		}
	}
	// PII 作为 filter / group_by 字段也必须拒绝。
	if _, _, err := s.BuildDistribution(DistQuery{
		Table: "player_basics", Column: "level",
		Filter: map[string]any{"player_id": 1},
	}); err == nil {
		t.Fatal("PII filter field must be rejected")
	}
	if _, _, err := s.BuildDistribution(DistQuery{
		Table: "player_basics", Column: "level",
		GroupBy: []string{"player_id"},
	}); err == nil {
		t.Fatal("PII group_by field must be rejected")
	}
}

// omitInLayer2SchemaYAML 是隔离的内存 schema：含一个非-PII 但 omit_in_layer2:true 的列，
// 专门覆盖 rejectIfPII(fd.PII || fd.OmitInLayer2) 的 omit_in_layer2 半边分支（解耦后此分支不再被共享 testSchemaYAML 覆盖）。
const omitInLayer2SchemaYAML = `
version: 1
domain: test
state_tables:
  t1:
    nature: snapshot
    primary_key: [id]
    fields:
      id:            {type: int64, role: actor_id}
      lvl:           {type: int16, role: level}
      internal_flag: {type: int32, role: dimension, omit_in_layer2: true}
`

func loadOmitSchema(t *testing.T) *Schema {
	t.Helper()
	s, err := Parse([]byte(omitInLayer2SchemaYAML))
	if err != nil {
		t.Fatalf("parse omit schema: %v", err)
	}
	return s
}

func TestBuildDistribution_RejectsOmitInLayer2Field(t *testing.T) {
	s := loadOmitSchema(t)
	// internal_flag 非 PII 但 omit_in_layer2:true — 不可查（rejectIfPII 的 OmitInLayer2 半边）。
	if _, _, err := s.BuildDistribution(DistQuery{
		Table: "t1", Column: "internal_flag", // 原始值模式（无 bucket_key）
	}); err == nil {
		t.Fatal("omit_in_layer2 column must be rejected as query column")
	} else if _, ok := err.(*SchemaError); !ok {
		t.Fatalf("expected *SchemaError, got %T", err)
	}
	// omit_in_layer2 作为 filter / group_by 字段也必须拒绝。
	if _, _, err := s.BuildDistribution(DistQuery{
		Table: "t1", Column: "lvl",
		Filter: map[string]any{"internal_flag": 1},
	}); err == nil {
		t.Fatal("omit_in_layer2 filter field must be rejected")
	}
	if _, _, err := s.BuildDistribution(DistQuery{
		Table: "t1", Column: "lvl",
		GroupBy: []string{"internal_flag"},
	}); err == nil {
		t.Fatal("omit_in_layer2 group_by field must be rejected")
	}
}

func TestBuildDistribution_NoFilter(t *testing.T) {
	s := loadTestSchema(t)
	sql, args, err := s.BuildDistribution(DistQuery{
		Table:     "player_currencies",
		Column:    "balance",
		BucketKey: "coins_balance",
		Filter:    nil,
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if strings.Contains(sql, "WHERE") {
		t.Fatalf("nil filter must not produce WHERE clause: %s", sql)
	}
	if len(args) != 0 {
		t.Fatalf("nil filter must produce empty args, got: %v", args)
	}
}

func TestBuildDistribution_FilterFieldWhitelist(t *testing.T) {
	s := loadTestSchema(t)
	_, _, err := s.BuildDistribution(DistQuery{
		Table:     "player_currencies",
		Column:    "balance",
		BucketKey: "coins_balance",
		Filter:    map[string]any{"not_a_field": 1},
	})
	if err == nil {
		t.Fatal("filter on undeclared field must be rejected")
	}
}

func TestBuildDistribution_BucketKeyMustExist(t *testing.T) {
	s := loadTestSchema(t)
	_, _, err := s.BuildDistribution(DistQuery{
		Table:     "player_currencies",
		Column:    "balance",
		BucketKey: "no_such_bucket",
	})
	if err == nil {
		t.Fatal("unknown bucket_key must be rejected")
	}
}

func TestBuildDistribution_TableAndColumnWhitelist(t *testing.T) {
	s := loadTestSchema(t)
	if _, _, err := s.BuildDistribution(DistQuery{Table: "evil", Column: "balance", BucketKey: "coins_balance"}); err == nil {
		t.Fatal("unknown table must be rejected")
	}
	if _, _, err := s.BuildDistribution(DistQuery{Table: "player_currencies", Column: "evil", BucketKey: "coins_balance"}); err == nil {
		t.Fatal("unknown column must be rejected")
	}
}

func TestBuildDistribution_FilterComparisonOp(t *testing.T) {
	s := loadTestSchema(t)
	sql, args, err := s.BuildDistribution(DistQuery{
		Table:     "player_currencies",
		Column:    "balance",
		BucketKey: "coins_balance",
		Filter:    map[string]any{"balance": map[string]any{"op": "<", "value": int64(100000)}},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !strings.Contains(sql, "balance < ?") {
		t.Fatalf("comparison op must render as 'balance < ?', got: %s", sql)
	}
	if len(args) != 1 || args[0] != int64(100000) {
		t.Fatalf("args mismatch: %v", args)
	}
}

func TestBuildDistribution_FilterScalarStillEquality(t *testing.T) {
	s := loadTestSchema(t)
	sql, _, err := s.BuildDistribution(DistQuery{
		Table:     "player_currencies",
		Column:    "balance",
		BucketKey: "coins_balance",
		Filter:    map[string]any{"currency_type": "coins"},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !strings.Contains(sql, "currency_type = ?") {
		t.Fatalf("scalar filter must stay equality, got: %s", sql)
	}
}

func TestBuildDistribution_FilterBadOpRejected(t *testing.T) {
	s := loadTestSchema(t)
	_, _, err := s.BuildDistribution(DistQuery{
		Table:     "player_currencies",
		Column:    "balance",
		BucketKey: "coins_balance",
		Filter:    map[string]any{"balance": map[string]any{"op": "; DROP TABLE x", "value": 1}},
	})
	if err == nil {
		t.Fatal("non-whitelisted op must be rejected")
	}
	if _, ok := err.(*SchemaError); !ok {
		t.Fatalf("expected *SchemaError, got %T", err)
	}
}

func TestBuildDistribution_FilterMissingValueRejected(t *testing.T) {
	s := loadTestSchema(t)
	_, _, err := s.BuildDistribution(DistQuery{
		Table:     "player_currencies",
		Column:    "balance",
		BucketKey: "coins_balance",
		Filter:    map[string]any{"balance": map[string]any{"op": "<"}},
	})
	if err == nil {
		t.Fatal("filter object without value must be rejected")
	}
	if _, ok := err.(*SchemaError); !ok {
		t.Fatalf("expected *SchemaError, got %T", err)
	}
}

func TestBuildDistribution_FilterNullValueRejected(t *testing.T) {
	s := loadTestSchema(t)
	_, _, err := s.BuildDistribution(DistQuery{
		Table:     "player_currencies",
		Column:    "balance",
		BucketKey: "coins_balance",
		Filter:    map[string]any{"balance": map[string]any{"op": "<", "value": nil}},
	})
	if err == nil {
		t.Fatal("null filter value must be rejected (would bind as SQL NULL)")
	}
	if _, ok := err.(*SchemaError); !ok {
		t.Fatalf("expected *SchemaError, got %T", err)
	}
}

// B：filter 值新增数组形（区间下钻）——同字段多条 AND 拼接。
func TestBuildDistribution_FilterRange(t *testing.T) {
	s := loadTestSchema(t)
	sql, args, err := s.BuildDistribution(DistQuery{
		Table:  "player_basics",
		Column: "level",
		Filter: map[string]any{
			"level": []any{
				map[string]any{"op": ">=", "value": int64(15)},
				map[string]any{"op": "<=", "value": int64(40)},
			},
		},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	// 两条都应进 WHERE，order 由 array 顺序固定
	if !strings.Contains(sql, "level >= ? AND level <= ?") {
		t.Fatalf("range filter must produce 'level >= ? AND level <= ?': %s", sql)
	}
	if len(args) != 2 || args[0] != int64(15) || args[1] != int64(40) {
		t.Fatalf("args = %v, want [15, 40]", args)
	}
}

func TestBuildDistribution_FilterEmptyArray(t *testing.T) {
	s := loadTestSchema(t)
	_, _, err := s.BuildDistribution(DistQuery{
		Table:  "player_basics",
		Column: "level",
		Filter: map[string]any{"level": []any{}},
	})
	if err == nil {
		t.Fatal("empty filter array must be rejected")
	}
}

func TestBuildDistribution_FilterArrayWithScalar(t *testing.T) {
	s := loadTestSchema(t)
	// 数组元素必须是 {op,value} 对象，标量数组拒绝（避免歧义）
	_, _, err := s.BuildDistribution(DistQuery{
		Table:  "player_basics",
		Column: "level",
		Filter: map[string]any{"level": []any{int64(15), int64(40)}},
	})
	if err == nil {
		t.Fatal("scalar elements in filter array must be rejected")
	}
}

func TestBuildDistribution_FilterArrayMixedWithEquality(t *testing.T) {
	s := loadTestSchema(t)
	// 区间 filter + 另一字段等值 filter 同时存在
	sql, args, err := s.BuildDistribution(DistQuery{
		Table:  "player_basics",
		Column: "level",
		Filter: map[string]any{
			"level": []any{
				map[string]any{"op": ">=", "value": int64(15)},
				map[string]any{"op": "<=", "value": int64(40)},
			},
			"server_id": int64(1),
		},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	// sortedKeys → "level" 先于 "server_id" 字典序排
	for _, want := range []string{"level >= ?", "level <= ?", "server_id = ?"} {
		if !strings.Contains(sql, want) {
			t.Fatalf("sql missing %q: %s", want, sql)
		}
	}
	if len(args) != 3 || args[0] != int64(15) || args[1] != int64(40) || args[2] != int64(1) {
		t.Fatalf("args = %v, want [15, 40, 1]", args)
	}
}
