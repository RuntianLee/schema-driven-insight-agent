// framework/eval_harness/evaluators/judge_test.go
package evaluators

import (
	"context"
	"fmt"
	"testing"
)

type flakyJudge struct {
	failN int
	calls int
	ok    string
}

func (f *flakyJudge) Call(_ context.Context, _ string) (string, int, int, float64, error) {
	f.calls++
	if f.calls <= f.failN {
		return "", 0, 0, 0, fmt.Errorf("transient judge error #%d", f.calls)
	}
	return f.ok, 0, 0, 0, nil
}
func (f *flakyJudge) Model() string { return "flaky-judge" }

func TestJudge_RetriesThenSucceeds(t *testing.T) {
	j := &judgeEvaluator{name: "reasoning_quality", intro: "x",
		client: &flakyJudge{failN: 2, ok: `{"score":4,"reason":"ok"}`}}
	spec := specNode(t, "rubric: r\nmin_score: 4")
	s, err := j.Evaluate(context.Background(), TaskResult{Answer: "a"}, spec)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if s.Errored {
		t.Fatalf("重试后应成功, got Errored")
	}
	if s.Value != 4.0/5.0 {
		t.Fatalf("score 应为 4/5, got %v", s.Value)
	}
}

func TestJudge_ExhaustedRetriesMarksErrored(t *testing.T) {
	j := &judgeEvaluator{name: "reasoning_quality", intro: "x",
		client: &flakyJudge{failN: 99, ok: `{"score":4,"reason":"ok"}`}}
	spec := specNode(t, "rubric: r\nmin_score: 4")
	s, err := j.Evaluate(context.Background(), TaskResult{Answer: "a"}, spec)
	if err != nil {
		t.Fatalf("evaluate 不应返回 error（错误须落在 Score.Errored）, got %v", err)
	}
	if !s.Errored {
		t.Fatalf("重试耗尽应标 Errored, got %+v", s)
	}
	if s.Value != 0 || s.Pass {
		t.Fatalf("errored score 应 Value=0/Pass=false, got %+v", s)
	}
	if s.BelowMin {
		t.Fatalf("errored score 的 BelowMin 应为 false（调用失败是不确定，不等同于评分低）, got %+v", s)
	}
}

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

func TestJudgeParseMarkdownFence(t *testing.T) {
	// 真 LLM 常包 markdown fence——应能剥离解析。
	e := NewReasoningQuality(constJudge("```json\n{\"score\":4,\"reason\":\"好\"}\n```"))
	s, err := e.Evaluate(context.Background(), TaskResult{Answer: "x"}, specNode(t, `rubric: "r"`))
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if s.Value != 0.8 { // 4/5
		t.Fatalf("fence 包裹的 JSON 应解析成功, got %+v", s)
	}
}

func TestJudgeScoreOutOfRange(t *testing.T) {
	// score 越界（prompt 约定 1-5）视为无效 → Value 0，不得产生 >1 的越界 Value。
	for _, raw := range []string{`{"score":7,"reason":"x"}`, `{"score":0,"reason":"x"}`, `{"score":-1,"reason":"x"}`} {
		e := NewReasoningQuality(constJudge(raw))
		s, err := e.Evaluate(context.Background(), TaskResult{Answer: "x"}, specNode(t, `rubric: "r"`))
		if err != nil {
			t.Fatalf("out-of-range must not error the suite: %v", err)
		}
		if s.Value != 0 {
			t.Fatalf("score 越界应记 0, got %+v (raw=%s)", s, raw)
		}
	}
}

func TestJudgeBelowMin(t *testing.T) {
	// mock judge 恒返 score=3
	// min_score: 4 → 3 < 4 → BelowMin 应为 true
	e := NewReasoningQuality(NewMockJudge())
	s, err := e.Evaluate(context.Background(), TaskResult{Answer: "x"}, specNode(t, `rubric: "r"
min_score: 4`))
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if !s.BelowMin {
		t.Fatalf("score 3 < min_score 4，BelowMin 应为 true，得 %+v", s)
	}

	// min_score: 3 → 3 不低于 3 → BelowMin 应为 false
	s2, err := e.Evaluate(context.Background(), TaskResult{Answer: "x"}, specNode(t, `rubric: "r"
min_score: 3`))
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if s2.BelowMin {
		t.Fatalf("score 3 = min_score 3，BelowMin 应为 false，得 %+v", s2)
	}
}

func TestJudgeParseFail_BelowMinTrue(t *testing.T) {
	// 解析失败 → BelowMin 应为 true（无法评分视为未达标）
	e := NewReasoningQuality(constJudge("not json"))
	s, err := e.Evaluate(context.Background(), TaskResult{Answer: "x"}, specNode(t, `rubric: "r"`))
	if err != nil {
		t.Fatalf("malformed json must not error the suite: %v", err)
	}
	if !s.BelowMin {
		t.Fatalf("解析失败应设 BelowMin=true，得 %+v", s)
	}
}
