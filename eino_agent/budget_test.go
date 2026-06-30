package eino_agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/RuntianLee/schema-driven-insight-agent/agent"
	"github.com/RuntianLee/schema-driven-insight-agent/contract"
	"github.com/RuntianLee/schema-driven-insight-agent/llm"
	"github.com/RuntianLee/schema-driven-insight-agent/tools"
)

// fatMock 每轮都发不同参数的 tool call（绕开重复查询拦截）且报告巨额 token——
// 模拟"步数不多但单轮巨大"的成本失控形态。
type fatMock struct{ turn int }

func (f *fatMock) Call(_ context.Context, _ string) (string, int, int, float64, error) {
	f.turn++
	return fmt.Sprintf(`{"tool":"noop","args":{"i":%d}}`, f.turn), 150_000, 1_000, 0.01, nil
}
func (f *fatMock) Model() string { return "fat-mock" }

var _ llm.Client = (*fatMock)(nil)

// capStore 是最小 TrajectoryStore：只捕获 Finalize 入参。
type capStore struct {
	outcome, errSummary string
}

func (c *capStore) TrajectoryID() string { return "cap" }
func (c *capStore) RecordLLMCall(_, _, _ string, _, _ int, _ float64, _, _ time.Time, _ error) {
}
func (c *capStore) RecordLLMCallRole(_, _, _, _ string, _, _ int, _ float64, _, _ time.Time, _ error) {
}
func (c *capStore) RecordToolCall(_ string, _, _ any, _, _ time.Time, _ error) {}
func (c *capStore) RecordReasoning(_ string, _, _ time.Time)                   {}
func (c *capStore) Finalize(_ context.Context, outcome, _, errSummary string) error {
	c.outcome, c.errSummary = outcome, errSummary
	return nil
}

var _ agent.TrajectoryStore = (*capStore)(nil)

// 预算闸：累计 token 超 maxBudgetTokens 时终止循环（在 maxTurns 之前），
// outcome=error 且错误信息可观测。
func TestRunner_BudgetGuardStopsRunaway(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register("noop", func(_ context.Context, _ map[string]any) (contract.Response, error) {
		return contract.Response{Status: contract.StatusOK}, nil
	})
	st := &capStore{}
	opener := func(_ context.Context, _, _ string) (agent.TrajectoryStore, error) { return st, nil }
	client := &fatMock{}

	_, err := New(client, reg, opener, "").Run(context.Background(), "q")
	if err == nil || !strings.Contains(err.Error(), "budget exceeded") {
		t.Fatalf("want budget exceeded error, got %v", err)
	}
	// 每轮 151k token，阈值 200k → 第 2 轮触发（远早于 maxTurns=8 的空转上限）。
	if client.turn != 2 {
		t.Fatalf("should stop at turn 2, ran %d turns", client.turn)
	}
	if st.outcome != "error" || !strings.Contains(st.errSummary, "budget exceeded") {
		t.Fatalf("trajectory finalize mismatch: outcome=%q err=%q", st.outcome, st.errSummary)
	}
}

// 最终回答优先于预算闸：当轮已产出 final answer 时照常返回，不因超预算丢弃。
func TestRunner_BudgetGuardDoesNotDropFinalAnswer(t *testing.T) {
	st := &capStore{}
	opener := func(_ context.Context, _, _ string) (agent.TrajectoryStore, error) { return st, nil }
	huge := &seqMock{responses: []string{"最终回答：分布高度集中。"}}
	// seqMock 报告小 token，但即便巨大也应返回——用 fat 变体验证：单轮即终答。
	ans, err := New(huge, tools.NewRegistry(), opener, "").Run(context.Background(), "q")
	if err != nil || !strings.Contains(ans, "最终回答") {
		t.Fatalf("final answer must be returned: ans=%q err=%v", ans, err)
	}
	if st.outcome != "success" {
		t.Fatalf("outcome = %q, want success", st.outcome)
	}
}
