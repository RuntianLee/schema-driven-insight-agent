// framework/eval_harness/evaluators/evaluator.go
// Package evaluators 定义统一 Evaluator 接口 + 三维实现（方案 A）。
// data_correctness 确定性进 CI gate；judge 类不进 gate。
package evaluators

import (
	"context"

	"github.com/RuntianLee/schema-driven-insight-agent/contract"
	"gopkg.in/yaml.v3"
)

// TaskResult 是一个任务跑完 agent 后、喂给 evaluator 的被测输出。
type TaskResult struct {
	TaskID    string
	Question  string
	Answer    string
	Outcome   string
	ToolCalls []contract.ToolCall
	RunErr    error
	// Advisory 是 Analyst→Advisor 流水线产出的建议草案；nil = 本任务未跑 Advisor。
	Advisory *contract.AdvisoryDraft
}

// Score 是单个 evaluator 对单个任务的评分。
// Value 归一 0..1（judge: score/5）；Pass 仅 Deterministic evaluator 参与 CI gate。
type Score struct {
	Evaluator string
	Value     float64
	Pass      bool
	BelowMin  bool   // judge 评分低于任务 min_score（仅 judge 类有意义；供 reflexion 选择性触发）
	Display   string // 报告人读："1.00 ✓" / "4/5 …"
	Detail    string // 失败明细 / judge reason
	Errored   bool   // judge 调用最终失败（已重试）；区别于"评分低/解析失败=0"，均值统计须排除而非折 0
}

// Evaluator 是三维评估器的统一接口。spec 是任务 YAML 里 evaluators.<name> 子树，
// 由各 evaluator 自行 Decode 到私有 spec struct。
type Evaluator interface {
	Name() string
	Deterministic() bool
	Evaluate(ctx context.Context, res TaskResult, spec *yaml.Node) (Score, error)
}

// Registry 按名持有 evaluator 实例（judge 类已注入 client）。
type Registry struct {
	m map[string]Evaluator
}

func NewRegistry() *Registry { return &Registry{m: make(map[string]Evaluator)} }

func (r *Registry) Register(e Evaluator) { r.m[e.Name()] = e }

func (r *Registry) Get(name string) (Evaluator, bool) {
	e, ok := r.m[name]
	return e, ok
}
