package eino_agent

import "testing"

func TestToolInfos(t *testing.T) {
	infos := ToolInfos()
	if len(infos) != 3 {
		t.Fatalf("want 3 tool infos, got %d", len(infos))
	}
	names := map[string]bool{}
	for _, ti := range infos {
		names[ti.Name] = true
		if ti.ParamsOneOf == nil {
			t.Errorf("tool %s missing ParamsOneOf", ti.Name)
		}
	}
	for _, want := range []string{"query_distribution", "analyze", "request_clarification"} {
		if !names[want] {
			t.Errorf("missing tool %q", want)
		}
	}
}

func TestToolInfos_IncludesRequestClarification(t *testing.T) {
	var found bool
	for _, ti := range ToolInfos() {
		if ti.Name == "request_clarification" {
			found = true
		}
	}
	if !found {
		t.Fatal("ToolInfos 应包含 request_clarification 声明")
	}
}
