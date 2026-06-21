// framework/eval_harness/evaluators/advisor_grounding_test.go
package evaluators

import (
	"context"
	"testing"

	"github.com/RuntianLee/schema-driven-insight-agent/contract"
	"gopkg.in/yaml.v3"
)

// ynode 将 YAML 字符串解析为 *yaml.Node（文档节点的第一个子节点）。
// 注意：本包已有 specNode（data_correctness_test.go），功能相同但签名报错信息略不同；
// 此处另立 ynode 供 advisor_grounding 测试专用，避免污染 specNode 的错误上下文。
func ynode(t *testing.T, s string) *yaml.Node {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(s), &n); err != nil {
		t.Fatal(err)
	}
	return n.Content[0]
}

func twoToolResult(adv *contract.AdvisoryDraft) TaskResult {
	return TaskResult{
		ToolCalls: []contract.ToolCall{{Name: "analyze"}, {Name: "query_distribution"}}, // → q1,q2
		Advisory:  adv,
	}
}

func TestAdvisorGroundingPass(t *testing.T) {
	adv := &contract.AdvisoryDraft{Items: []contract.Recommendation{
		{SourceRef: "q1", Action: "a"}, {SourceRef: "q2", Action: "b"},
	}}
	s, err := NewAdvisorGrounding().Evaluate(context.Background(), twoToolResult(adv), ynode(t, "min_items: 1"))
	if err != nil {
		t.Fatal(err)
	}
	if !s.Pass || s.Value != 1.0 {
		t.Errorf("want pass/1.0, got pass=%v value=%v detail=%q", s.Pass, s.Value, s.Detail)
	}
}

func TestAdvisorGroundingFailsOnBadRefOrEmpty(t *testing.T) {
	bad := &contract.AdvisoryDraft{Items: []contract.Recommendation{{SourceRef: "q9", Action: "x"}}}
	s, _ := NewAdvisorGrounding().Evaluate(context.Background(), twoToolResult(bad), ynode(t, "min_items: 1"))
	if s.Pass {
		t.Error("伪造 SourceRef 应 gate 失败")
	}

	s2, _ := NewAdvisorGrounding().Evaluate(context.Background(), twoToolResult(nil), ynode(t, "min_items: 1"))
	if s2.Pass {
		t.Error("无 Advisory（流水线未跑）应 gate 失败")
	}
}
