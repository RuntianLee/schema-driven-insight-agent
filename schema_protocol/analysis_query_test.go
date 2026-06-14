package schema_protocol

import "testing"

func TestValidIdent(t *testing.T) {
	ok := []string{"players", "avg_money", "n", "lv_kinds"}
	// NOTE: "drop" was removed from bad — it matches ^[a-z][a-z0-9_]*$ and is a valid
	// identifier; SQL-keyword filtering is not in scope (injection is prevented by the
	// regex whitelist + parameterized SQL; aliases are never used as table/column names).
	bad := []string{"", "1abc", "AS x", "a;b", "Players", "a-b", "a b"}
	for _, s := range ok {
		if !validIdent(s) {
			t.Errorf("validIdent(%q)=false want true", s)
		}
	}
	for _, s := range bad {
		if validIdent(s) {
			t.Errorf("validIdent(%q)=true want false", s)
		}
	}
}

func TestWhitelists(t *testing.T) {
	for _, fn := range []string{"count", "count_distinct", "sum", "avg", "min", "max"} {
		if !aggFns[fn] {
			t.Errorf("aggFns missing %q", fn)
		}
	}
	if aggFns["median"] || aggFns["percentile"] || aggFns["ntile"] {
		t.Error("aggFns 不应含 median/percentile/ntile（spec K6）")
	}
	for _, op := range []string{"=", "!=", "<", "<=", ">", ">="} {
		if !comparisonOps[op] {
			t.Errorf("comparisonOps missing %q", op)
		}
	}
}
