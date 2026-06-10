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
