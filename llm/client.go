// Package llm 抽象 LLM provider。Client 接口的返回值对齐 trajectory.Recorder.RecordLLMCall。
package llm

import "context"

type Client interface {
	Call(ctx context.Context, prompt string) (resp string, tokIn, tokOut int, costUSD float64, err error)
	Model() string
}

// JudgeProfiler 由能按 judge 需求克隆调教参数的 Client 实现。
// eval_harness 经此窄接口套 judge profile，无需硬断言到具体 client 类型。
type JudgeProfiler interface {
	// WithJudgeProfile 返回带 judge 调教参数的副本（不可变：原 client 不变）。
	WithJudgeProfile(maxTokens int, disableThinking bool) Client
}
