package eino_agent

import (
	"strings"
	"testing"
)

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

func TestClarifyToolDescCaliberUniqueness(t *testing.T) {
	// C1b：工具声明 Desc 与 prompt 的「口径唯一性自检」措辞对齐。
	var desc string
	for _, ti := range ToolInfos() {
		if ti.Name == "request_clarification" {
			desc = ti.Desc
		}
	}
	for _, want := range []string{"口径无法唯一确定", "取值集"} {
		if !strings.Contains(desc, want) {
			t.Fatalf("clarify Desc 缺 %q（C1b 措辞对齐）", want)
		}
	}
}
