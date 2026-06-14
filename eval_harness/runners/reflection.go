// framework/eval_harness/runners/reflection.go
package runners

import "context"

// ReflectionProvider 是 reflection 增益 A/B 度量的不透明接缝（V2 子项 #5 定义，#4 实现）。
// 给定任务上下文，返回要注入 agent 本轮的额外上下文（如过往洞察 / 轨迹经验）。
// 返回空串 = 无注入 = 与 baseline 等价。Config.ReflectionProvider 为 nil（默认）即 reflection 关。
//
// 机制无关：A/B 编排器只 diff「configB 是否 wire 了 provider」；具体 reflection
// 机制（Memory 检索 / self-critique / 轨迹复用）全在 #4 实现，在本接缝之后。
type ReflectionProvider interface {
	ContextFor(ctx context.Context, taskID, question string) (string, error)
}
