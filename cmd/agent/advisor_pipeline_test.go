// cmd/agent/advisor_pipeline_test.go
package main

import (
	"context"
	"strings"
	"testing"

	"github.com/RuntianLee/schema-driven-insight-agent/contract"
	"github.com/RuntianLee/schema-driven-insight-agent/eino_agent"
	"github.com/RuntianLee/schema-driven-insight-agent/tools"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// seqLLM 按序返回脚本响应（Analyst 工具轮 → Analyst 答案 → Advisor 草案）。
type seqLLM struct {
	resps []string
	i     int
}

func (s *seqLLM) Call(_ context.Context, _ string) (string, int, int, float64, error) {
	r := s.resps[s.i]
	if s.i < len(s.resps)-1 {
		s.i++
	}
	return r, 0, 0, 0, nil
}
func (s *seqLLM) Model() string { return "seq" }

// scriptedModel 按脚本顺序返回 *schema.Message，实现 model.ToolCallingChatModel（Agent 腿的 analyst）。
type scriptedModel struct {
	resps []*schema.Message
	i     int
}

func (m *scriptedModel) Generate(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	r := m.resps[m.i]
	if m.i < len(m.resps)-1 {
		m.i++
	}
	return r, nil
}
func (m *scriptedModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	panic("scriptedModel.Stream unused")
}
func (m *scriptedModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) { return m, nil }

var _ model.ToolCallingChatModel = (*scriptedModel)(nil)

func TestRunAdvisePipeline(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register("analyze", func(_ context.Context, _ map[string]any) (contract.Response, error) {
		return contract.Response{Status: contract.StatusOK}, nil
	})
	// client（seqLLM）迁移后只喂 ADVISOR：仅一条草案 JSON。
	client := &seqLLM{resps: []string{
		`{"summary":"建议草案","items":[{"observation":"德国流失高","source_ref":"q1","action":"排查德国体验","priority":"high","caveat":"草案"}]}`,
	}}

	// analyst 现在走 Agent 腿的 eino ChatModel：先出 tool_call 轮，再出最终答案。
	analyst := &scriptedModel{resps: []*schema.Message{
		{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: "c1", Type: "function",
			Function: schema.FunctionCall{Name: "analyze", Arguments: `{"table":"customers"}`}}},
			ResponseMeta: &schema.ResponseMeta{Usage: &schema.TokenUsage{PromptTokens: 1, CompletionTokens: 1}}},
		{Role: schema.Assistant, Content: "德国流失显著偏高。",
			ResponseMeta: &schema.ResponseMeta{Usage: &schema.TokenUsage{PromptTokens: 1, CompletionTokens: 1}}},
	}}

	answer, draft, err := runAdvisePipeline(context.Background(), client, analyst, "seq", reg, "SCHEMA-DIGEST", "playbook 文本", "各国客户流失？", eino_agent.NonInteractiveClarifier{})
	if err != nil {
		t.Fatalf("pipeline: %v", err)
	}
	if !strings.Contains(answer, "德国") {
		t.Errorf("analyst answer=%q", answer)
	}
	if len(draft.Items) != 1 || draft.Items[0].SourceRef != "q1" {
		t.Fatalf("draft=%+v", draft)
	}

	out := renderAdvisory(draft)
	if !strings.Contains(out, "排查德国体验") || !strings.Contains(out, "建议草案") {
		t.Errorf("render=%q", out)
	}
}

func TestRenderAdvisoryEmpty(t *testing.T) {
	out := renderAdvisory(contract.AdvisoryDraft{Summary: "测试", Items: nil})
	if !strings.Contains(out, "无可靠依据") {
		t.Errorf("empty items fallback missing, got: %q", out)
	}
}
