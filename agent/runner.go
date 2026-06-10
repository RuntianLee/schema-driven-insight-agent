// Package agent 是 provider-agnostic 的纯行为接口层（design-v3 §4 三接口 D5）。
// 只 import contract（数据形状），0 实现包依赖。换 LLM 不换 agent 逻辑。
package agent

import "context"

// Runner 管理一次任务的循环与生命周期。
type Runner interface {
	Run(ctx context.Context, question string) (string, error)
	Health(ctx context.Context) error
	Stop(ctx context.Context) error
}
