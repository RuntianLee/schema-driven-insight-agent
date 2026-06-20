package reflexion

import (
	"context"
	"strings"
	"testing"

	"github.com/RuntianLee/schema-driven-insight-agent/contract"
	"github.com/RuntianLee/schema-driven-insight-agent/eval_harness/evaluators"
)

// fakeReflectLLM：固定返回一条经验串，记录是否被调用。
type fakeReflectLLM struct {
	out   string
	calls int
}

func (f *fakeReflectLLM) Model() string { return "fake-reflect" }
func (f *fakeReflectLLM) Call(_ context.Context, _ string) (string, int, int, float64, error) {
	f.calls++
	return f.out, 1, 1, 0, nil
}

func failRes(taskID string) evaluators.TaskResult {
	return evaluators.TaskResult{
		TaskID:   taskID,
		Question: "各服玩家数？",
		Answer:   "（错答）",
		ToolCalls: []contract.ToolCall{
			{Name: "analyze", Args: map[string]any{"group_by": []any{"wrong_field"}},
				Response: contract.Response{Status: contract.StatusSchemaError}},
		},
	}
}

func TestProvider_ColdContextIsEmpty(t *testing.T) {
	p := New(&fakeReflectLLM{out: "经验X"})
	got, err := p.ContextFor(context.Background(), "t1", "问题")
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("冷起应无经验，得 %q", got)
	}
}

func TestProvider_ObserveFail_ThenContextHasLesson(t *testing.T) {
	llm := &fakeReflectLLM{out: "下次该用 server_id 分组"}
	p := New(llm)
	if err := p.Observe(context.Background(), failRes("t1"), map[string]evaluators.Score{
		"data_correctness": {Pass: false},
	}); err != nil {
		t.Fatal(err)
	}
	if llm.calls != 1 {
		t.Fatalf("失败应触发 1 次 reflect LLM 调用，得 %d", llm.calls)
	}
	got, _ := p.ContextFor(context.Background(), "t1", "问题")
	if !strings.Contains(got, "下次该用 server_id 分组") {
		t.Fatalf("ContextFor 应含累积经验，得 %q", got)
	}
}

func TestProviderObserveAndUpdateReturnsFixQueryObservation(t *testing.T) {
	llm := &fakeReflectLLM{out: "下次先确认过滤口径。"}
	p := New(llm)
	obs, err := p.observeAndUpdate(context.Background(), failRes("t1"), map[string]evaluators.Score{
		"data_correctness": {Evaluator: "data_correctness", Pass: false},
	})
	if err != nil {
		t.Fatal(err)
	}
	if obs.mode != observationFixQuery {
		t.Fatalf("mode=%q want %q", obs.mode, observationFixQuery)
	}
	if obs.lesson != "下次先确认过滤口径。" {
		t.Fatalf("lesson=%q", obs.lesson)
	}
	if llm.calls != 1 {
		t.Fatalf("reflect llm calls=%d want 1", llm.calls)
	}
}

func TestProvider_ObservePass_NoLesson(t *testing.T) {
	llm := &fakeReflectLLM{out: "不该出现"}
	p := New(llm)
	if err := p.Observe(context.Background(), failRes("t1"), map[string]evaluators.Score{
		"data_correctness":  {Pass: true},
		"reasoning_quality": {Pass: true},
	}); err != nil {
		t.Fatal(err)
	}
	if llm.calls != 0 {
		t.Fatalf("通过不应触发 reflect（省成本），得 %d 次调用", llm.calls)
	}
	got, _ := p.ContextFor(context.Background(), "t1", "问题")
	if got != "" {
		t.Fatalf("通过不应累积经验，得 %q", got)
	}
}

func TestProvider_Reset_ClearsLessons(t *testing.T) {
	p := New(&fakeReflectLLM{out: "经验"})
	_ = p.Observe(context.Background(), failRes("t1"), map[string]evaluators.Score{
		"data_correctness": {Pass: false},
	})
	p.Reset()
	got, _ := p.ContextFor(context.Background(), "t1", "问题")
	if got != "" {
		t.Fatalf("Reset 后应无经验，得 %q", got)
	}
}

func TestProvider_PromptHasNoGolden(t *testing.T) {
	prompt := buildReflectPrompt(failRes("t1"))
	if !strings.Contains(prompt, "各服玩家数？") {
		t.Fatal("prompt 应含任务问题")
	}
	if strings.Contains(strings.ToLower(prompt), "expect") {
		t.Fatal("prompt 不应含 golden 期望字样")
	}
}

func TestProvider_NoRefine_WhenReasoningOK(t *testing.T) {
	llm := &fakeReflectLLM{out: "不该被调用"}
	p := New(llm)
	res := evaluators.TaskResult{
		TaskID:    "t1",
		Question:  "各服玩家数？",
		Answer:    "（解读已充分）",
		ToolCalls: []contract.ToolCall{{Name: "analyze", Args: map[string]any{"group_by": []any{"server_id"}}}},
	}
	// data_correctness 通过、reasoning_quality 未低于阈值（BelowMin=false）→ 不应进 refine 模式
	scores := map[string]evaluators.Score{
		"data_correctness":  {Pass: true},
		"reasoning_quality": {BelowMin: false, Detail: "解读充分"},
	}
	if err := p.Observe(context.Background(), res, scores); err != nil {
		t.Fatal(err)
	}
	if llm.calls != 0 {
		t.Fatalf("不应调 reflect LLM，得 %d 次", llm.calls)
	}
	got, _ := p.ContextFor(context.Background(), "t1", "各服玩家数？")
	if got != "" {
		t.Fatalf("reasoning 达标时不应注入 refine 上下文，得 %q", got)
	}
}

func TestProvider_RefineMode_QueryCorrectReasoningWeak(t *testing.T) {
	llm := &fakeReflectLLM{out: "不该被调用"}
	p := New(llm)
	res := evaluators.TaskResult{
		TaskID:   "t1",
		Question: "各服玩家数？",
		Answer:   "（过于简略的解读）",
		ToolCalls: []contract.ToolCall{
			{Name: "analyze", Args: map[string]any{"group_by": []any{"server_id"}}},
		},
	}
	// data_correctness 通过、reasoning_quality 低于阈值（BelowMin=true）→ refine-explanation 模式
	scores := map[string]evaluators.Score{
		"data_correctness":  {Pass: true},
		"reasoning_quality": {BelowMin: true, Detail: "未给运营含义"},
	}
	if err := p.Observe(context.Background(), res, scores); err != nil {
		t.Fatal(err)
	}
	if llm.calls != 0 {
		t.Fatalf("refine 模式不应调 reflect LLM，得 %d 次", llm.calls)
	}
	got, _ := p.ContextFor(context.Background(), "t1", "各服玩家数？")
	if !strings.Contains(got, "查询是正确的") || !strings.Contains(got, "analyze") {
		t.Fatalf("ContextFor 应指引复用正确查询，得 %q", got)
	}
	if !strings.Contains(got, "未给运营含义") {
		t.Fatalf("ContextFor 应带上 reasoning 反馈，得 %q", got)
	}
}

func TestProviderObserveAndUpdateReturnsRefineObservation(t *testing.T) {
	llm := &fakeReflectLLM{out: "不该被调用"}
	p := New(llm)
	res := evaluators.TaskResult{
		TaskID:   "t1",
		Question: "各服玩家数？",
		Answer:   "（过于简略的解读）",
		ToolCalls: []contract.ToolCall{
			{Name: "analyze", Args: map[string]any{"group_by": []any{"server_id"}}},
		},
	}
	obs, err := p.observeAndUpdate(context.Background(), res, map[string]evaluators.Score{
		"data_correctness":  {Pass: true},
		"reasoning_quality": {BelowMin: true, Detail: "未给运营含义"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if obs.mode != observationRefineExplanation {
		t.Fatalf("mode=%q want %q", obs.mode, observationRefineExplanation)
	}
	if obs.refine.feedback != "未给运营含义" {
		t.Fatalf("feedback=%q", obs.refine.feedback)
	}
	if llm.calls != 0 {
		t.Fatalf("refine should not call reflect llm, got %d", llm.calls)
	}
}
