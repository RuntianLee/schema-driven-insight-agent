package eino_agent

import (
	"context"
	"sync"
	"time"

	"github.com/RuntianLee/schema-driven-insight-agent/agent"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// fakeModel 按脚本顺序返回 *schema.Message；实现 model.ToolCallingChatModel。
type fakeModel struct {
	responses []*schema.Message
	calls     int
}

func (f *fakeModel) Generate(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	r := f.responses[f.calls]
	f.calls++
	return r, nil
}

func (f *fakeModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	panic("fakeModel.Stream unused")
}

func (f *fakeModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return f, nil
}

// asMsg 构造一条带 tool_calls 的 assistant 消息。
func asMsg(usageIn, usageOut int, calls ...schema.ToolCall) *schema.Message {
	return &schema.Message{
		Role:         schema.Assistant,
		ToolCalls:    calls,
		ResponseMeta: &schema.ResponseMeta{Usage: &schema.TokenUsage{PromptTokens: usageIn, CompletionTokens: usageOut}},
	}
}

// finalMsg 构造一条纯文本最终答案。
func finalMsg(content string) *schema.Message {
	return &schema.Message{Role: schema.Assistant, Content: content,
		ResponseMeta: &schema.ResponseMeta{Usage: &schema.TokenUsage{PromptTokens: 1, CompletionTokens: 1}}}
}

func tc(id, name, argsJSON string) schema.ToolCall {
	return schema.ToolCall{ID: id, Type: "function", Function: schema.FunctionCall{Name: name, Arguments: argsJSON}}
}

// fakeRecorder 实现 agent.TrajectoryStore，记录调用以供断言。
type fakeRecorder struct {
	mu        sync.Mutex
	llmCalls  int
	toolCalls []string // tool 名顺序
	finalOut  string
	outcome   string
}

func (r *fakeRecorder) TrajectoryID() string { return "fake" }
func (r *fakeRecorder) RecordLLMCall(_, _, _ string, _, _ int, _ float64, _, _ time.Time, _ error) {
	r.mu.Lock()
	r.llmCalls++
	r.mu.Unlock()
}
func (r *fakeRecorder) RecordLLMCallRole(_, _, _, _ string, _, _ int, _ float64, _, _ time.Time, _ error) {
}
func (r *fakeRecorder) RecordToolCall(name string, _, _ any, _, _ time.Time, _ error) {
	r.mu.Lock()
	r.toolCalls = append(r.toolCalls, name)
	r.mu.Unlock()
}
func (r *fakeRecorder) RecordReasoning(string, time.Time, time.Time) {}
func (r *fakeRecorder) Finalize(_ context.Context, outcome, finalOutput, _ string) error {
	r.outcome = outcome
	r.finalOut = finalOutput
	return nil
}

// 编译期断言：确保 fake 实现满足接口。
var _ model.ToolCallingChatModel = (*fakeModel)(nil)
var _ agent.TrajectoryStore = (*fakeRecorder)(nil)
