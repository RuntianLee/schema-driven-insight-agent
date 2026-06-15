// framework/eval_harness/runners/reflection.go
package runners

import (
	"context"

	"github.com/RuntianLee/schema-driven-insight-agent/eval_harness/evaluators"
)

// ReflectionProvider 是 reflection 增益 A/B 度量的不透明接缝（V2 子项 #5 定义，#4 实现）。
// 给定任务上下文，返回要注入 agent 本轮的额外上下文（如过往洞察 / 轨迹经验）。
// 返回空串 = 无注入 = 与 baseline 等价。Config.ReflectionProvider 为 nil（默认）即 reflection 关。
//
// 机制无关：A/B 编排器只 diff「configB 是否 wire 了 provider」；具体 reflection
// 机制（Memory 检索 / self-critique / 轨迹复用）全在 #4 实现，在本接缝之后。
type ReflectionProvider interface {
	ContextFor(ctx context.Context, taskID, question string) (string, error)
}

// ReflectionObserver 是 reflection 的【可选】回写接缝（V2 子项 #4）：实现它的
// ReflectionProvider 会在 agent 跑完某任务、评分后收到自己的轨迹结果 + 二值成败
// （data_correctness 是否通过），用于蒸出过程经验供后续 trial 注入。
// RunSuite 类型断言调用；未实现者路径字节不变（向后兼容，nil/baseline 不触发）。
//
// 关键不变量：res 是 agent 自己的产出（tool 调用 / 状态 / answer），结构性不含
// evaluator 的 golden 期望表——无泄漏。
type ReflectionObserver interface {
	Observe(ctx context.Context, res evaluators.TaskResult, passed bool) error
}
