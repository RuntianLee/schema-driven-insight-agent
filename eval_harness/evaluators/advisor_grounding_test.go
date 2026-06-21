// framework/eval_harness/evaluators/advisor_grounding_test.go
package evaluators

import (
	"context"
	"testing"

	"github.com/RuntianLee/schema-driven-insight-agent/contract"
)

func twoToolResult(adv *contract.AdvisoryDraft) TaskResult {
	return TaskResult{
		ToolCalls: []contract.ToolCall{{Name: "analyze"}, {Name: "query_distribution"}}, // → q1,q2
		Advisory:  adv,
	}
}

func TestAdvisorGrounding_DeterministicAndName(t *testing.T) {
	e := NewAdvisorGrounding()
	if e.Name() != "advisor_grounding" || !e.Deterministic() {
		t.Fatalf("unexpected: %s det=%v", e.Name(), e.Deterministic())
	}
}

func TestAdvisorGroundingPass(t *testing.T) {
	adv := &contract.AdvisoryDraft{Items: []contract.Recommendation{
		{SourceRef: "q1", Action: "a"}, {SourceRef: "q2", Action: "b"},
	}}
	s, err := NewAdvisorGrounding().Evaluate(context.Background(), twoToolResult(adv), specNode(t, "min_items: 1"))
	if err != nil {
		t.Fatal(err)
	}
	if !s.Pass || s.Value != 1.0 {
		t.Errorf("want pass/1.0, got pass=%v value=%v detail=%q", s.Pass, s.Value, s.Detail)
	}
}

func TestAdvisorGroundingFailsOnBadRefOrEmpty(t *testing.T) {
	bad := &contract.AdvisoryDraft{Items: []contract.Recommendation{{SourceRef: "q9", Action: "x"}}}
	s, _ := NewAdvisorGrounding().Evaluate(context.Background(), twoToolResult(bad), specNode(t, "min_items: 1"))
	if s.Pass {
		t.Error("伪造 SourceRef 应 gate 失败")
	}

	s2, _ := NewAdvisorGrounding().Evaluate(context.Background(), twoToolResult(nil), specNode(t, "min_items: 1"))
	if s2.Pass {
		t.Error("无 Advisory（流水线未跑）应 gate 失败")
	}
}
