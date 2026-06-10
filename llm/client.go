// Package llm 抽象 LLM provider。Client 接口的返回值对齐 trajectory.Recorder.RecordLLMCall。
package llm

import "context"

type Client interface {
	Call(ctx context.Context, prompt string) (resp string, tokIn, tokOut int, costUSD float64, err error)
	Model() string
}
