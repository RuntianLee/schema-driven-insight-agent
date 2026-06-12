package seedgen

import (
	"strings"
	"testing"
)

const specYAML = `
as_of: 1717000000
tables:
  player_basics:
    rows: 1000
    columns:
      coins:
        enum:
          - {value: 50, weight: 600}
          - {value: 500, weight: 400}
      level: {buckets: [{min: 1, max: 30}]}
      last_login: {const: 1700000000}
`

func TestParseSpec(t *testing.T) {
	sp, err := ParseSpec([]byte(specYAML))
	if err != nil {
		t.Fatal(err)
	}
	if sp.AsOf != 1717000000 || sp.Seed != DefaultSeed {
		t.Errorf("头字段: %+v", sp)
	}
	tb := sp.Tables["player_basics"]
	if tb.Rows != 1000 || len(tb.Columns) != 3 {
		t.Errorf("table spec: %+v", tb)
	}
}

func TestParseSpec_Validation(t *testing.T) {
	cases := []struct{ name, find, replace, wantErr string }{
		{"rows 必填", "rows: 1000", "rows: 0", "rows"},
		{"生成器三选一", "{const: 1700000000}", "{const: 1700000000, buckets: [{min: 1, max: 2}]}", "生成器"},
		{"bucket min<=max", "{min: 1, max: 30}", "{min: 30, max: 1}", "min"},
		{"skew 白名单", "{min: 1, max: 30}", "{min: 1, max: 30, skew: wild}", "skew"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			y := strings.Replace(specYAML, c.find, c.replace, 1)
			if y == specYAML {
				t.Fatalf("replace 未命中: %q", c.find)
			}
			if _, err := ParseSpec([]byte(y)); err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("want %q, got %v", c.wantErr, err)
			}
		})
	}
}
