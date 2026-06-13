package csvload

import "testing"

func TestCoerce(t *testing.T) {
	cases := []struct {
		cell, typ string
		want      any
		wantErr   bool
	}{
		{"France", "TEXT", "France", false},
		{"42", "INTEGER", int64(42), false},
		{"83807.86", "INTEGER", int64(83808), false}, // 浮点货币四舍五入
		{"0", "INTEGER", int64(0), false},
		{" 7 ", "INTEGER", int64(7), false}, // 去空白
		{"", "INTEGER", nil, true},          // 空单元格 fail-fast
		{"abc", "INTEGER", nil, true},
	}
	for _, c := range cases {
		got, err := coerce(c.cell, c.typ)
		if c.wantErr {
			if err == nil {
				t.Errorf("coerce(%q,%q) 期望 err，得 %v", c.cell, c.typ, got)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("coerce(%q,%q) = %v,%v；want %v", c.cell, c.typ, got, err, c.want)
		}
	}
}
