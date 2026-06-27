// framework/eval_harness/evaluators/advisor_grounding_test.go
package evaluators

import (
	"context"
	"testing"

	"github.com/RuntianLee/schema-driven-insight-agent/contract"
)

func twoToolResult(adv *contract.AdvisoryDraft) TaskResult {
	return TaskResult{
		// 两个 status=OK 调用 → q1,q2（OK-only 口径，2026-06-27 (b') 统一）。
		ToolCalls: []contract.ToolCall{
			{Name: "analyze", Response: contract.Response{Status: contract.StatusOK}},
			{Name: "query_distribution", Response: contract.Response{Status: contract.StatusOK}},
		},
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

func TestQIndexCadence_Unified_OKOnly(t *testing.T) {
	calls := []contract.ToolCall{
		{Name: "analyze", Response: contract.Response{Status: contract.StatusSchemaError}},
		{Name: "analyze", Response: contract.Response{Status: contract.StatusOK,
			Profile: &contract.DistProfile{Mean: 100}}},
		{Name: "analyze", Response: contract.Response{Status: contract.StatusOK,
			Profile: &contract.DistProfile{Mean: 200}}},
	}
	got1, err := Resolve(calls, "q1.profile.mean")
	if err != nil || got1 != 100 {
		t.Fatalf("q1.profile.mean 应=100，得 %v err %v", got1, err)
	}
	got2, err := Resolve(calls, "q2.profile.mean")
	if err != nil || got2 != 200 {
		t.Fatalf("q2.profile.mean 应=200，得 %v err %v", got2, err)
	}
	if _, err := Resolve(calls, "q3.profile.mean"); err == nil {
		t.Fatal("q3 应越界（仅 2 个成功结果）")
	}
}
