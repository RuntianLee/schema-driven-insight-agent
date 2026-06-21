// advisor/advisor_test.go
package advisor

import (
	"context"
	"testing"

	"github.com/RuntianLee/schema-driven-insight-agent/contract"
)

// scriptLLM 是返回固定响应的 llm.Client 桩。
type scriptLLM struct{ resp string }

func (s scriptLLM) Call(_ context.Context, _ string) (string, int, int, float64, error) {
	return s.resp, 0, 0, 0, nil
}
func (s scriptLLM) Model() string { return "script" }

func sampleOutput() contract.AnalystOutput {
	return contract.AnalystOutput{
		Question: "各国客户流失？",
		Results: []contract.AnalystResult{
			{ID: "q1", Call: contract.ToolCall{Name: "query_distribution"}},
		},
		Narrative: "德国流失显著偏高。",
	}
}

func TestAdviseKeepsGroundedDropsHallucinated(t *testing.T) {
	resp := `{"summary":"建议草案","items":[
		{"observation":"德国流失高","source_ref":"q1","action":"排查德国体验","priority":"high","caveat":"草案"},
		{"observation":"凭空捏造","source_ref":"q9","action":"乱建议","priority":"low","caveat":"草案"}
	]}`
	a := New(scriptLLM{resp: resp}, "SYS")
	draft, err := a.Advise(context.Background(), sampleOutput(), "playbook 文本")
	if err != nil {
		t.Fatalf("Advise: %v", err)
	}
	if len(draft.Items) != 1 {
		t.Fatalf("应只保留 1 条溯源有效条目，实得 %d", len(draft.Items))
	}
	if draft.Items[0].SourceRef != "q1" {
		t.Errorf("SourceRef=%q want q1", draft.Items[0].SourceRef)
	}
}

func TestAdviseEmptyOnNoJSON(t *testing.T) {
	a := New(scriptLLM{resp: "抱歉我无法作答"}, "SYS")
	_, err := a.Advise(context.Background(), sampleOutput(), "")
	if err == nil {
		t.Fatal("无 JSON 应报错")
	}
}
