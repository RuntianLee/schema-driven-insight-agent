package eino_agent

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/RuntianLee/schema-driven-insight-agent/agent"
	"github.com/RuntianLee/schema-driven-insight-agent/contract"
)

// stubClarifier 记录收到的问题、返回预设答复。
type stubClarifier struct {
	gotQuestion string
	reply       string
}

func (c *stubClarifier) Ask(_ context.Context, question string) string {
	c.gotQuestion = question
	return c.reply
}

func newTestRunnerWithClarifier(m *capturingModel, disp agent.ToolDispatcher, clar Clarifier) *Runner {
	opener := func(context.Context, string, string) (agent.TrajectoryStore, error) { return &fakeRecorder{}, nil }
	return New(m, "test", disp, opener, "", clar)
}

// TestClarify_InterceptedAndFedBack：request_clarification 被拦截、Clarifier 收到问题、
// 答复作为 tool_result 喂回下一轮模型输入。
func TestClarify_InterceptedAndFedBack(t *testing.T) {
	m := &capturingModel{responses: []*schema.Message{
		asMsg(1, 1, tc("cl", "request_clarification", `{"question":"你指虚拟货币还是金币？"}`)),
		finalMsg("已按虚拟货币作答。"),
	}}
	disp := &stubDispatcher{resp: map[string]contract.Response{}}
	clar := &stubClarifier{reply: "虚拟货币"}
	r := newTestRunnerWithClarifier(m, disp, clar)

	ans, err := r.Run(context.Background(), "分布怎样？")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if ans != "已按虚拟货币作答。" {
		t.Fatalf("answer=%q", ans)
	}
	if clar.gotQuestion != "你指虚拟货币还是金币？" {
		t.Fatalf("clarifier 未收到模型的问句，got=%q", clar.gotQuestion)
	}
	turn2In := flattenToolMsgs(m.seen[1])
	if !strings.Contains(turn2In, "虚拟货币") {
		t.Fatalf("答复未喂回下一轮，轮2输入:\n%s", turn2In)
	}
}

// TestClarify_NotCountedInQIndex：clarify 轮不占 q 编号。轮1 clarify → 轮2 真数据 OK 应得 q1。
func TestClarify_NotCountedInQIndex(t *testing.T) {
	m := &capturingModel{responses: []*schema.Message{
		asMsg(1, 1, tc("cl", "request_clarification", `{"question":"哪种货币？"}`)),
		asMsg(1, 1, tc("c1", "query_distribution", `{"table":"t","column":"balance"}`)),
		finalMsg("done"),
	}}
	disp := &stubDispatcher{resp: map[string]contract.Response{
		"query_distribution": {Status: contract.StatusOK},
	}}
	r := newTestRunnerWithClarifier(m, disp, &stubClarifier{reply: "虚拟货币"})
	if _, err := r.Run(context.Background(), "q"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	turn3In := flattenToolMsgs(m.seen[2])
	if !strings.Contains(turn3In, "结果 id: q1") {
		t.Errorf("首个数据 OK 应得 q1，轮3输入:\n%s", turn3In)
	}
	if strings.Contains(turn3In, "结果 id: q2") {
		t.Errorf("clarify 轮不应占 q 编号，轮3输入:\n%s", turn3In)
	}
}
