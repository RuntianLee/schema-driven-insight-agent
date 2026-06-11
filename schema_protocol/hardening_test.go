package schema_protocol

import (
	"strings"
	"testing"
)

// bucket label 含单引号：必须翻倍转义后 inline，不得破坏 SQL 字面量。
func TestBuildDistribution_LabelQuoteEscaped(t *testing.T) {
	yaml := strings.Replace(testSchemaYAML, `label: "0~1w"`, `label: "don't"`, 1)
	s, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	sql, _, err := s.BuildDistribution(DistQuery{
		Table:     "player_currencies",
		Column:    "balance",
		BucketKey: "coins_balance",
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !strings.Contains(sql, "'don''t'") {
		t.Fatalf("label 单引号未转义: %s", sql)
	}
}

func TestQuoteSQLString(t *testing.T) {
	cases := map[string]string{
		"plain":      "'plain'",
		"don't":      "'don''t'",
		"'; DROP --": "'''; DROP --'",
	}
	for in, want := range cases {
		if got := quoteSQLString(in); got != want {
			t.Errorf("quoteSQLString(%q) = %q, want %q", in, got, want)
		}
	}
}

// 表名/列名必须是安全 SQL 标识符（defense in depth：白名单之外再校验名字本身）。
func TestParse_RejectsMaliciousIdentifiers(t *testing.T) {
	badColumn := `
version: 1
domain: test
state_tables:
  player_basics:
    fields:
      "bal; DROP TABLE x--": {type: int64, role: balance}
`
	if _, err := Parse([]byte(badColumn)); err == nil {
		t.Fatal("恶意列名必须被 Parse 拒绝")
	} else if !strings.Contains(err.Error(), "非法列名") {
		t.Fatalf("错误信息应指明非法列名，got: %v", err)
	}

	badTable := `
version: 1
domain: test
state_tables:
  "players; --":
    fields:
      level: {type: int32, role: level}
`
	if _, err := Parse([]byte(badTable)); err == nil {
		t.Fatal("恶意表名必须被 Parse 拒绝")
	} else if !strings.Contains(err.Error(), "非法表名") {
		t.Fatalf("错误信息应指明非法表名，got: %v", err)
	}

	// 合法 schema 不受影响
	if _, err := Parse([]byte(testSchemaYAML)); err != nil {
		t.Fatalf("合法 schema 不应被拒: %v", err)
	}
}
