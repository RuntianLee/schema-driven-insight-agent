package eino_agent

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/RuntianLee/schema-driven-insight-agent/agent"
	"github.com/RuntianLee/schema-driven-insight-agent/contract"
)

// capturingModel 记录每轮收到的 msgs，并按序返回脚本响应，用于断言注入内容。
type capturingModel struct {
	responses []*schema.Message
	i         int
	seen      [][]*schema.Message // 每次 Generate 收到的 msgs 快照
}

func (m *capturingModel) Generate(_ context.Context, in []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	cp := make([]*schema.Message, len(in))
	copy(cp, in)
	m.seen = append(m.seen, cp)
	r := m.responses[m.i]
	m.i++
	return r, nil
}

func (m *capturingModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	panic("capturingModel.Stream unused")
}

func (m *capturingModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

// runnerWith 用给定的 model 和 dispatcher 组装 Runner（测试专用）。
func runnerWith(m model.ToolCallingChatModel, disp agent.ToolDispatcher) *Runner {
	opener := func(context.Context, string, string) (agent.TrajectoryStore, error) { return &fakeRecorder{}, nil }
	return New(m, "test", disp, opener, "", NonInteractiveClarifier{})
}

// flattenToolMsgs 只收集 tool_result 消息（Role==Tool）的 Content 拼成一个字符串，
// 排除 system/user/assistant 消息，避免 system prompt 里的示例文本干扰断言。
func flattenToolMsgs(msgs []*schema.Message) string {
	var b strings.Builder
	for _, mm := range msgs {
		if mm.Role == schema.Tool {
			b.WriteString(mm.Content)
			b.WriteString("\n")
		}
	}
	return b.String()
}

// statusSeqDispatcher 按序返回预设状态（每次 Dispatch 取下一个，末尾保持最后值）。
type statusSeqDispatcher struct {
	statuses []contract.Status
	i        int
}

func (d *statusSeqDispatcher) Dispatch(_ context.Context, _ string, _ map[string]any) (contract.Response, error) {
	s := d.statuses[d.i]
	if d.i < len(d.statuses)-1 {
		d.i++
	}
	return contract.Response{Status: s}, nil
}

// TestConservation_QIndexOnlyOnOK 验证两条守恒性质：
//  1. q{N} 标签仅在 StatusOK 结果的 tool_result content 中出现；
//  2. okSeq 仅对 StatusOK 递增——前置非 OK 不占编号，第一个 OK 得 q1 而非 q2。
func TestConservation_QIndexOnlyOnOK(t *testing.T) {
	// 轮1：调 analyze → SCHEMA_ERROR（非 OK，不应有 q 标签，不应 okSeq++）
	// 轮2：调 analyze（换参）→ OK（应得 q1，不是 q2）
	// 轮3：最终答案（无 tool_calls）
	m := &capturingModel{responses: []*schema.Message{
		asMsg(1, 1, tc("c1", "analyze", `{"table":"t1"}`)),
		asMsg(1, 1, tc("c2", "analyze", `{"table":"t2"}`)),
		finalMsg("done"),
	}}
	disp := &statusSeqDispatcher{
		statuses: []contract.Status{contract.StatusSchemaError, contract.StatusOK},
	}
	if _, err := runnerWith(m, disp).Run(context.Background(), "q"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// m.seen[1] 是轮2的输入，含轮1的 tool_result（SCHEMA_ERROR）→ 不应有 q 标签。
	turn2In := flattenToolMsgs(m.seen[1])
	if strings.Contains(turn2In, "结果 id: q") {
		t.Errorf("非 OK 结果不应注入 q 标签，但轮2输入含之:\n%s", turn2In)
	}
	if !strings.Contains(turn2In, "不计入结果编号") {
		t.Errorf("非 OK 结果应含「不计入结果编号」反馈，轮2输入:\n%s", turn2In)
	}

	// m.seen[2] 是轮3的输入，含轮2的 tool_result（OK）→ 应有 q1，不应有 q2。
	turn3In := flattenToolMsgs(m.seen[2])
	if !strings.Contains(turn3In, "结果 id: q1") {
		t.Errorf("第一个 OK 结果应得 q1，轮3输入:\n%s", turn3In)
	}
	if strings.Contains(turn3In, "结果 id: q2") {
		t.Errorf("okSeq 不应因非 OK 结果而跳到 q2，轮3输入:\n%s", turn3In)
	}
}

// hintDispatcher 返回带 Hint 的 SCHEMA_ERROR，用于验证 hint 回注自修正路径。
type hintDispatcher struct{ hint string }

func (d hintDispatcher) Dispatch(_ context.Context, _ string, _ map[string]any) (contract.Response, error) {
	return contract.Response{Status: contract.StatusSchemaError, Hint: d.hint}, nil
}

// TestConservation_SchemaErrorHintFedBack 验证 SCHEMA_ERROR 的 hint + 修正提示回注下一轮模型输入。
func TestConservation_SchemaErrorHintFedBack(t *testing.T) {
	const hint = "unknown field 'filter'; did you mean 'filters'?"
	m := &capturingModel{responses: []*schema.Message{
		asMsg(1, 1, tc("c1", "analyze", `{"table":"t","filter":{}}`)),
		finalMsg("修正后答案"),
	}}
	if _, err := runnerWith(m, hintDispatcher{hint: hint}).Run(context.Background(), "q"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// m.seen[1] = 第二轮输入，含第一轮的 SCHEMA_ERROR tool_result。
	turn2Tools := flattenToolMsgs(m.seen[1])
	if !strings.Contains(turn2Tools, hint) {
		t.Errorf("SCHEMA_ERROR 的 hint 未回注下一轮，tool 消息:\n%s", turn2Tools)
	}
	if !strings.Contains(turn2Tools, "请修正后重试") {
		t.Errorf("缺自修正提示「请修正后重试」，tool 消息:\n%s", turn2Tools)
	}
	if !strings.Contains(turn2Tools, "不计入结果编号") {
		t.Errorf("非 OK 结果应标「不计入结果编号」，tool 消息:\n%s", turn2Tools)
	}
}
