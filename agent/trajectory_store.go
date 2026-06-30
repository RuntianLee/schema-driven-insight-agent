package agent

import (
	"context"
	"time"
)

// TrajectoryStore 持久化执行轨迹，内部强制 Redact（PII 边界）。
// 实现 = framework/trajectory.Recorder（trajectory-spec-v2 §6），靠结构化类型桥接。
type TrajectoryStore interface {
	TrajectoryID() string
	RecordLLMCall(prompt, response, model string, tokensIn, tokensOut int,
		costUSD float64, started, ended time.Time, err error)
	RecordLLMCallRole(role, prompt, response, model string, tokensIn, tokensOut int,
		costUSD float64, started, ended time.Time, err error)
	RecordToolCall(toolName string, input, output any, started, ended time.Time, err error)
	RecordReasoning(thought string, started, ended time.Time)
	Finalize(ctx context.Context, outcome, finalOutput, errSummary string) error
}
