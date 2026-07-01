// framework/eval_harness/runners/mock_seq.go
package runners

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/RuntianLee/schema-driven-insight-agent/llm"
)

// sequencedMock 实现 llm.Client：按调用次序回放 task.llm_turns，不靠子串匹配。
// 解决 llm.NewMock 在多轮累积对话里的歧义 panic（spec §3.1）。
type sequencedMock struct {
	turns []string
	i     int
}

func newSequencedMock(turns []string) *sequencedMock { return &sequencedMock{turns: turns} }

func (m *sequencedMock) Call(_ context.Context, prompt string) (string, int, int, float64, error) {
	if m.i >= len(m.turns) {
		return "", 0, 0, 0, fmt.Errorf(
			"sequencedMock exhausted after %d turns: 任务 llm_turns 未覆盖全部 LLM 轮次（检查 happy-path 假设/SCHEMA_ERROR 自纠）",
			len(m.turns))
	}
	r := m.turns[m.i]
	m.i++
	return r, len(prompt) / 4, len(r) / 4, 0, nil
}

func (m *sequencedMock) Model() string { return "mock-seq" }

// 编译期断言：满足 llm.Client。
var _ llm.Client = (*sequencedMock)(nil)

// scriptedModel 是 eval mock 道的确定性 agent 模型：按序把 turns 作为 assistant message.Content
// 返回（无结构化 ToolCalls），由 Runner 的 fallback 探测器链解析文本工具调用——保持迁移前 mock
// 道行为不变。耗尽后钳在最后一条（同 sequencedMock 语义）。
type scriptedModel struct {
	turns []string
	i     int
}

func newScriptedModel(turns []string) *scriptedModel { return &scriptedModel{turns: turns} }

func (m *scriptedModel) Generate(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	content := ""
	if len(m.turns) > 0 {
		content = m.turns[m.i]
		if m.i < len(m.turns)-1 {
			m.i++
		}
	}
	return &schema.Message{Role: schema.Assistant, Content: content}, nil
}

func (m *scriptedModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	panic("scriptedModel.Stream unused")
}

func (m *scriptedModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) { return m, nil }

// 编译期断言：满足 model.ToolCallingChatModel。
var _ model.ToolCallingChatModel = (*scriptedModel)(nil)
