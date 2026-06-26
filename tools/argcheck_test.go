package tools

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/RuntianLee/schema-driven-insight-agent/contract"
)

func TestDerivedWhitelists(t *testing.T) {
	gotA := append([]string(nil), analyzeKnownKeys...)
	sort.Strings(gotA)
	wantA := []string{"aggregates", "filters", "group_by", "having", "limit", "order_by", "table"}
	if !reflect.DeepEqual(gotA, wantA) {
		t.Fatalf("analyzeKnownKeys=%v want %v", gotA, wantA)
	}
	gotQ := append([]string(nil), queryDistributionKnownKeys...)
	sort.Strings(gotQ)
	wantQ := []string{"bucket_key", "column", "filter", "group_by", "table"}
	if !reflect.DeepEqual(gotQ, wantQ) {
		t.Fatalf("queryDistributionKnownKeys=%v want %v", gotQ, wantQ)
	}
}

func TestParseAnalyzeArgs_UnknownKeyFilterSuggestsFilters(t *testing.T) {
	_, resp := ParseAnalyzeArgs(map[string]any{
		"table":  "customers",
		"filter": map[string]any{"field": "Balance", "op": ">", "value": 150000},
	})
	if resp == nil || resp.Status != contract.StatusSchemaError {
		t.Fatalf("want SCHEMA_ERROR, got %+v", resp)
	}
	if !strings.Contains(resp.Hint, `"filter"`) || !strings.Contains(resp.Hint, `did you mean "filters"`) {
		t.Fatalf("hint must flag filter→filters, got %q", resp.Hint)
	}
}

func TestParseQueryDistributionArgs_UnknownKeyFiltersSuggestsFilter(t *testing.T) {
	_, resp := ParseQueryDistributionArgs(map[string]any{
		"table":   "player_basics",
		"column":  "level",
		"filters": []any{},
	})
	if resp == nil || resp.Status != contract.StatusSchemaError {
		t.Fatalf("want SCHEMA_ERROR, got %+v", resp)
	}
	if !strings.Contains(resp.Hint, `did you mean "filter"`) {
		t.Fatalf("hint must suggest filter, got %q", resp.Hint)
	}
}

func TestParseAnalyzeArgs_AllKnownKeysPass(t *testing.T) {
	in, resp := ParseAnalyzeArgs(map[string]any{
		"table":      "customers",
		"filters":    []any{map[string]any{"field": "Balance", "op": ">", "value": 150000}},
		"group_by":   []any{"Exited"},
		"aggregates": []any{map[string]any{"fn": "count", "as": "n"}},
		"having":     []any{},
		"order_by":   []any{},
		"limit":      float64(10),
	})
	if resp != nil {
		t.Fatalf("all-known keys must not error, got %+v", resp)
	}
	if in.Table != "customers" || len(in.Filters) != 1 || in.Limit != 10 {
		t.Fatalf("parse wrong: %+v", in)
	}
}

func TestParseQueryDistributionArgs_AllKnownKeysPass(t *testing.T) {
	in, resp := ParseQueryDistributionArgs(map[string]any{
		"table":      "player_currencies",
		"column":     "balance",
		"bucket_key": "coins_balance",
		"filter":     map[string]any{"currency_type": "coins"},
		"group_by":   []any{"server_id"},
	})
	if resp != nil {
		t.Fatalf("all-known keys must not error, got %+v", resp)
	}
	if in.Table != "player_currencies" || in.Column != "balance" {
		t.Fatalf("parse wrong: %+v", in)
	}
}

func TestParseAnalyzeArgs_FarUnknownKeyListsValidKeys(t *testing.T) {
	_, resp := ParseAnalyzeArgs(map[string]any{"table": "customers", "sort": "asc"})
	if resp == nil || resp.Status != contract.StatusSchemaError {
		t.Fatalf("want SCHEMA_ERROR, got %+v", resp)
	}
	if !strings.Contains(resp.Hint, "valid keys") || !strings.Contains(resp.Hint, "aggregates") {
		t.Fatalf("far unknown must list valid keys, got %q", resp.Hint)
	}
}

func TestParseAnalyzeArgs_MultipleUnknownKeysAllListed(t *testing.T) {
	_, resp := ParseAnalyzeArgs(map[string]any{"table": "customers", "filter": map[string]any{}, "xyz": 1})
	if resp == nil {
		t.Fatal("want SCHEMA_ERROR")
	}
	if !strings.Contains(resp.Hint, `"filter"`) || !strings.Contains(resp.Hint, `"xyz"`) {
		t.Fatalf("both unknown keys must appear, got %q", resp.Hint)
	}
}

func TestLevenshtein(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"filter", "filters", 1},
		{"aggregate", "aggregates", 1},
		{"sort", "order_by", 7},
		{"", "abc", 3},
		{"same", "same", 0},
	}
	for _, c := range cases {
		if got := levenshtein(c.a, c.b); got != c.want {
			t.Errorf("levenshtein(%q,%q)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}
