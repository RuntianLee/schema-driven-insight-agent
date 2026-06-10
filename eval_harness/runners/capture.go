// framework/eval_harness/runners/capture.go
package runners

import (
	"context"
	"fmt"
	"time"

	"github.com/RuntianLee/schema-driven-insight-agent/agent"
	"github.com/RuntianLee/schema-driven-insight-agent/contract"
	"github.com/RuntianLee/schema-driven-insight-agent/eval_harness/evaluators"
)

// captureStore 实现 agent.TrajectoryStore，把 trajectory 捕获进内存（不落 SQLite）。
// eino_agent.Runner 零改动：靠注入此 store 的 opener 拿到 tool 输出 + 最终答案。
//
// 诚实性：eval 用 mock LLM（sequencedMock，model="mock-seq"），经 teeStore 落库后
// trajectory_steps 的 llm_call token/cost 为 len/4 估算、非真实值。成本/token 聚合务必
// WHERE task_class='production' 过滤掉 benchmark 行；data_correctness 走真 tool（连
// fixture），verdict 可信——这正是跨版本对比的有效信号。
type captureStore struct {
	toolCalls []evaluators.ToolCall
	outcome   string
	finalOut  string
}

func newCaptureStore() *captureStore { return &captureStore{} }

func (c *captureStore) TrajectoryID() string { return "eval-capture" }

func (c *captureStore) RecordLLMCall(_, _, _ string, _, _ int, _ float64, _, _ time.Time, _ error) {
}

func (c *captureStore) RecordToolCall(name string, input, output any, _, _ time.Time, err error) {
	tc := evaluators.ToolCall{Name: name, Err: err}
	// eino_agent 的 ToolDispatcher 始终以 map[string]any 传 tool 入参（解析自 LLM 的 tool-call JSON），
	// 故此断言恒成立；非 map 入参（理论上不出现）则 Args 留 nil，不致命。
	if m, ok := input.(map[string]any); ok {
		tc.Args = m
	}
	if resp, ok := output.(contract.Response); ok {
		tc.Response = resp
	} else if err == nil {
		// R2：dispatch 未报错但 output 不是 Response → 记录类型错误，不 panic。
		tc.Err = fmt.Errorf("captureStore: tool %q output 非 contract.Response（实际 %T）", name, output)
	}
	c.toolCalls = append(c.toolCalls, tc)
}

func (c *captureStore) RecordReasoning(_ string, _, _ time.Time) {}

func (c *captureStore) Finalize(_ context.Context, outcome, finalOutput, _ string) error {
	c.outcome = outcome
	c.finalOut = finalOutput
	return nil
}

// 编译期断言：满足 agent.TrajectoryStore。
var _ agent.TrajectoryStore = (*captureStore)(nil)
