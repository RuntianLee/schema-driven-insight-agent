// framework/eval_harness/evaluators/judge_test.go
package evaluators

import (
	"context"
	"testing"
)

func TestMockJudgeReturnsPlaceholder(t *testing.T) {
	c := NewMockJudge()
	resp, _, _, _, err := c.Call(context.Background(), "任意 judge prompt")
	if err != nil {
		t.Fatalf("mock judge call: %v", err)
	}
	if resp != `{"score":3,"reason":"[mock] 管道连通"}` {
		t.Fatalf("unexpected mock judge resp: %q", resp)
	}
}

func TestReasoningQualityParsesJudge(t *testing.T) {
	e := NewReasoningQuality(NewMockJudge())
	if e.Name() != "reasoning_quality" || e.Deterministic() {
		t.Fatalf("unexpected: %s det=%v", e.Name(), e.Deterministic())
	}
	spec := specNode(t, `rubric: "是否量化集中度？"`)
	res := TaskResult{Answer: "流失集中在 20 级（60%）"}
	s, err := e.Evaluate(context.Background(), res, spec)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if s.Value != 0.6 { // 3/5
		t.Fatalf("want value 0.6 (3/5), got %g", s.Value)
	}
	if s.Pass {
		t.Fatalf("judge 不参与 gate，Pass 应恒 false")
	}
}

func TestInsightNoveltyName(t *testing.T) {
	if NewInsightNovelty(NewMockJudge()).Name() != "insight_novelty" {
		t.Fatalf("unexpected name")
	}
}

func TestJudgeParseMalformedJSON(t *testing.T) {
	// R3：解析失败 → Value 0，不 panic、不报错中断。
	e := NewReasoningQuality(constJudge("not json"))
	s, err := e.Evaluate(context.Background(), TaskResult{Answer: "x"}, specNode(t, `rubric: "r"`))
	if err != nil {
		t.Fatalf("malformed json must not error the suite: %v", err)
	}
	if s.Value != 0 || s.Detail == "" {
		t.Fatalf("want value 0 + detail, got %+v", s)
	}
}
