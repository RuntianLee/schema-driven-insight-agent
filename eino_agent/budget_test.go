package eino_agent

import (
	"context"
	"strings"
	"testing"

	"github.com/RuntianLee/schema-driven-insight-agent/contract"
	"github.com/cloudwego/eino/schema"
)

func TestRun_BudgetExceeded(t *testing.T) {
	// 单轮巨大 usage：超 maxBudgetTokens 即终止（error）。
	huge := asMsg(maxBudgetTokens+1, 0, tc("c", "analyze", `{"table":"t"}`))
	model := &fakeModel{responses: []*schema.Message{huge, finalMsg("never")}}
	disp := &stubDispatcher{resp: map[string]contract.Response{"analyze": {Status: contract.StatusOK}}}
	_, err := newTestRunner(model, disp, &fakeRecorder{}).Run(context.Background(), "q")
	if err == nil || !strings.Contains(err.Error(), "budget exceeded") {
		t.Fatalf("want budget error, got %v", err)
	}
}

func TestRun_MaxTurns(t *testing.T) {
	// 模型每轮都换参发新工具（永不给答案）→ maxTurns 后 error。
	// 提供 maxTurns+1 条响应：循环跑满 maxTurns 轮（每轮消耗一条），第 maxTurns+1 条不会被读到。
	var resp []*schema.Message
	for i := 0; i < maxTurns+1; i++ {
		resp = append(resp, asMsg(1, 1, tc("c", "analyze", `{"table":"t","column":"`+string(rune('a'+i))+`"}`)))
	}
	model := &fakeModel{responses: resp}
	disp := &stubDispatcher{resp: map[string]contract.Response{"analyze": {Status: contract.StatusOK}}}
	_, err := newTestRunner(model, disp, &fakeRecorder{}).Run(context.Background(), "q")
	if err == nil || !strings.Contains(err.Error(), "max turns") {
		t.Fatalf("want max turns error, got %v", err)
	}
}
