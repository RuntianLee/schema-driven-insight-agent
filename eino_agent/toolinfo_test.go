package eino_agent

import "testing"

func TestToolInfos(t *testing.T) {
	infos := ToolInfos()
	if len(infos) != 2 {
		t.Fatalf("want 2 tool infos, got %d", len(infos))
	}
	names := map[string]bool{}
	for _, ti := range infos {
		names[ti.Name] = true
		if ti.ParamsOneOf == nil {
			t.Errorf("tool %s missing ParamsOneOf", ti.Name)
		}
	}
	for _, want := range []string{"query_distribution", "analyze"} {
		if !names[want] {
			t.Errorf("missing tool %q", want)
		}
	}
}
