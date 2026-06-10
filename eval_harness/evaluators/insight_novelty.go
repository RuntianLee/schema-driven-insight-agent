// framework/eval_harness/evaluators/insight_novelty.go
package evaluators

import "github.com/RuntianLee/schema-driven-insight-agent/llm"

// NewInsightNovelty 评估洞察新颖度（是否超越数字罗列、给出二阶解读/建议）。
func NewInsightNovelty(client llm.Client) Evaluator {
	return &judgeEvaluator{
		name:   "insight_novelty",
		intro:  "你是数据分析评审官，评估回答的洞察新颖度：是否超越数字罗列、给出原因推断或可行动建议。",
		client: client,
	}
}
