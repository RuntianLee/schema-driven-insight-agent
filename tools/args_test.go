// framework/tools/args_test.go
package tools

import "testing"

func TestArgsToQueryDistributionInput(t *testing.T) {
	args := map[string]any{
		"table":    "player_basics",
		"column":   "level",
		"filter":   map[string]any{"last_online_time": map[string]any{"op": "<", "value": 100}},
		"group_by": []any{"server_id"},
	}
	in := ArgsToQueryDistributionInput(args)
	if in.Table != "player_basics" || in.Column != "level" {
		t.Fatalf("table/column not mapped: %+v", in)
	}
	if len(in.GroupBy) != 1 || in.GroupBy[0] != "server_id" {
		t.Fatalf("group_by not mapped: %+v", in.GroupBy)
	}
	if in.Filter["last_online_time"] == nil {
		t.Fatalf("filter not mapped")
	}
}

// TestArgsToQueryDistributionInput_BucketKey verifies the bucket_key field mapping.
func TestArgsToQueryDistributionInput_BucketKey(t *testing.T) {
	args := map[string]any{
		"table":      "player_basics",
		"bucket_key": "week",
	}
	in := ArgsToQueryDistributionInput(args)
	if in.BucketKey != "week" {
		t.Fatalf("bucket_key not mapped: %+v", in)
	}
}

// TestArgsToQueryDistributionInput_GroupByStringSlice verifies that a []string group_by
// (real private helper handles this branch, e.g. when caller already has []string) is also
// mapped correctly.
func TestArgsToQueryDistributionInput_GroupByStringSlice(t *testing.T) {
	args := map[string]any{
		"table":    "player_basics",
		"group_by": []string{"server_id", "level"},
	}
	in := ArgsToQueryDistributionInput(args)
	if len(in.GroupBy) != 2 || in.GroupBy[0] != "server_id" || in.GroupBy[1] != "level" {
		t.Fatalf("group_by []string not mapped: %+v", in.GroupBy)
	}
}

// TestArgsToQueryDistributionInput_Empty verifies zero-value on empty args.
func TestArgsToQueryDistributionInput_Empty(t *testing.T) {
	in := ArgsToQueryDistributionInput(map[string]any{})
	if in.Table != "" || in.Column != "" || in.BucketKey != "" || in.Filter != nil || in.GroupBy != nil {
		t.Fatalf("expected zero-value input, got %+v", in)
	}
}

func TestArgsToAnalyzeInput(t *testing.T) {
	args := map[string]any{
		"table":    "player_basics",
		"group_by": []any{"server_id", "level"},
		"filters": []any{
			map[string]any{"field": "server_id", "op": "IN", "values": []any{float64(1), float64(2)}},
			map[string]any{"field": "level", "op": ">=", "value": float64(15)},
		},
		"aggregates": []any{
			map[string]any{"fn": "count", "as": "players"},
			map[string]any{"fn": "avg", "column": "virtual_money", "as": "avg_money"},
		},
		"having":   []any{map[string]any{"alias": "players", "op": ">", "value": float64(100)}},
		"order_by": []any{map[string]any{"key": "players", "desc": true}},
		"limit":    float64(50),
	}
	in := ArgsToAnalyzeInput(args)
	if in.Table != "player_basics" || in.Limit != 50 {
		t.Fatalf("table/limit wrong: %+v", in)
	}
	if len(in.GroupBy) != 2 || in.GroupBy[1] != "level" {
		t.Fatalf("group_by wrong: %v", in.GroupBy)
	}
	if len(in.Filters) != 2 || in.Filters[0].Op != "IN" || len(in.Filters[0].Values) != 2 {
		t.Fatalf("filters wrong: %+v", in.Filters)
	}
	if in.Filters[1].Op != ">=" || in.Filters[1].Value != float64(15) {
		t.Fatalf("scalar filter wrong: %+v", in.Filters[1])
	}
	if len(in.Aggregates) != 2 || in.Aggregates[1].Fn != "avg" || in.Aggregates[1].Column != "virtual_money" {
		t.Fatalf("aggregates wrong: %+v", in.Aggregates)
	}
	if len(in.Having) != 1 || in.Having[0].Alias != "players" {
		t.Fatalf("having wrong: %+v", in.Having)
	}
	if len(in.OrderBy) != 1 || !in.OrderBy[0].Desc || in.OrderBy[0].Key != "players" {
		t.Fatalf("order_by wrong: %+v", in.OrderBy)
	}
}
