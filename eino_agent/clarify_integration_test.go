package eino_agent

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/RuntianLee/schema-driven-insight-agent/contract"
)

// TestClarify_NonInteractiveDegradesToAnswer 验证 eval 道闭环：
// 模型发 clarify → NonInteractiveClarifier 恒降级 → 降级串喂回 → 模型据此产最终答案。
func TestClarify_NonInteractiveDegradesToAnswer(t *testing.T) {
	m := &capturingModel{responses: []*schema.Message{
		asMsg(1, 1, tc("cl", "request_clarification", `{"question":"哪种货币？"}`)),
		finalMsg("本次假设：货币=虚拟货币。\n分布如下。"),
	}}
	disp := &stubDispatcher{resp: map[string]contract.Response{}}
	r := newTestRunnerWithClarifier(m, disp, NonInteractiveClarifier{})

	ans, err := r.Run(context.Background(), "分布怎样？")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(ans, "本次假设") {
		t.Fatalf("非交互道应产带假设的最终答案，got=%q", ans)
	}
	// 降级串应作为 tool_result 喂回第二轮。
	turn2In := flattenToolMsgs(m.seen[1])
	if !strings.Contains(turn2In, "非交互模式") {
		t.Fatalf("降级串未喂回，轮2输入:\n%s", turn2In)
	}
}
