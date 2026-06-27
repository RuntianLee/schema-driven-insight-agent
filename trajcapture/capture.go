// trajcapture/capture.go

// Package trajcapture 提供把 agent 轨迹捕获进内存的 TrajectoryStore（Capture）+ 双扇 Tee。
// eino_agent.Runner 零改：靠注入返回这些 store 的 opener，即可拿到结构化工具结果。
// eval 与 cmd/agent 的 Advisor 流水线共用（DRY）。
package trajcapture

import (
	"context"
	"fmt"
	"time"

	"github.com/RuntianLee/schema-driven-insight-agent/agent"
	"github.com/RuntianLee/schema-driven-insight-agent/contract"
)

// Capture 实现 agent.TrajectoryStore，把工具调用 + 结局捕获进内存（不落 SQLite）。
type Capture struct {
	toolCalls []contract.ToolCall
	outcome   string
	finalOut  string
}

func New() *Capture { return &Capture{} }

func (c *Capture) TrajectoryID() string { return "capture" }

func (c *Capture) RecordLLMCall(_, _, _ string, _, _ int, _ float64, _, _ time.Time, _ error) {}

func (c *Capture) RecordToolCall(name string, input, output any, _, _ time.Time, err error) {
	tc := contract.ToolCall{Name: name, Err: err}
	// eino_agent 的 ToolDispatcher 始终以 map[string]any 传入参；非 map（理论不出现）则 Args 留 nil。
	if m, ok := input.(map[string]any); ok {
		tc.Args = m
	}
	if resp, ok := output.(contract.Response); ok {
		tc.Response = resp
	} else if err == nil {
		tc.Err = fmt.Errorf("trajcapture: tool %q output 非 contract.Response（实际 %T）", name, output)
	}
	c.toolCalls = append(c.toolCalls, tc)
}

func (c *Capture) RecordReasoning(_ string, _, _ time.Time) {}

func (c *Capture) Finalize(_ context.Context, outcome, finalOutput, _ string) error {
	c.outcome = outcome
	c.finalOut = finalOutput
	return nil
}

// ToolCalls / Outcome / FinalOutput 暴露捕获结果（eval suite + 流水线读）。
func (c *Capture) ToolCalls() []contract.ToolCall { return c.toolCalls }
func (c *Capture) Outcome() string                { return c.outcome }
func (c *Capture) FinalOutput() string            { return c.finalOut }

// AnalystResults 把捕获的**成功(OK)**工具调用转成带稳定 ID（q1,q2,…）的 AnalystResult，
// 供 Advisor 引用。口径与 attribution.Resolve / advisor_grounding 统一（contract.OKCalls）。
func (c *Capture) AnalystResults() []contract.AnalystResult {
	ok := contract.OKCalls(c.toolCalls)
	out := make([]contract.AnalystResult, len(ok))
	for i, tc := range ok {
		out[i] = contract.AnalystResult{ID: contract.AnalystResultID(i), Call: tc}
	}
	return out
}

var _ agent.TrajectoryStore = (*Capture)(nil)
