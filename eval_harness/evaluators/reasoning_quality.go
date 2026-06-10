// framework/eval_harness/evaluators/reasoning_quality.go
package evaluators

import "github.com/RuntianLee/schema-driven-insight-agent/llm"

// NewReasoningQuality 评估推理质量（是否识别分布特征并量化）。
func NewReasoningQuality(client llm.Client) Evaluator {
	return &judgeEvaluator{
		name:   "reasoning_quality",
		intro:  "你是数据分析评审官，评估回答的推理质量：是否基于数据得出结论、是否量化关键信号。",
		client: client,
	}
}
