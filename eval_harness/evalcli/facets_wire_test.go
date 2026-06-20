package evalcli

import (
	"testing"

	"github.com/RuntianLee/schema-driven-insight-agent/eval_harness/tasks"
)

func TestFacetsForTaskPrefersOverrideThenDerives(t *testing.T) {
	override := tasks.Task{Facets: []string{"shape:sentinel"}}
	if got := facetsForTask(override); len(got) != 1 || got[0] != "shape:sentinel" {
		t.Fatalf("override 应优先, got %v", got)
	}
	derived := tasks.Task{LLMTurns: []string{`{"tool":"analyze","args":{"group_by":["server_id"],"aggregates":[{"fn":"avg","column":"x","as":"m"}]}}`}}
	got := facetsForTask(derived)
	if !containsStr(got, "shape:mean") || !containsStr(got, "agg:avg") {
		t.Fatalf("派生应得 mean/avg, got %v", got)
	}
}

func containsStr(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
