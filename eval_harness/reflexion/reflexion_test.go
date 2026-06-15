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
		ToolCalls: []evaluators.ToolCall{
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
	if err := p.Observe(context.Background(), failRes("t1"), false); err != nil {
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

func TestProvider_ObservePass_NoLesson(t *testing.T) {
	llm := &fakeReflectLLM{out: "不该出现"}
	p := New(llm)
	if err := p.Observe(context.Background(), failRes("t1"), true); err != nil {
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
	_ = p.Observe(context.Background(), failRes("t1"), false)
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
