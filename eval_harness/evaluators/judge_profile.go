// framework/eval_harness/evaluators/judge_profile.go
package evaluators

import "github.com/RuntianLee/schema-driven-insight-agent/llm"

// judgeMaxTokens 是 judge LLM 调用的预算（容「thinking + 正文」）。
// 框架内部正确性常量，非用户调的延迟/成本旋钮（那是 agent 的 config max_tokens）。
const judgeMaxTokens = 8000

// JudgeProfile 声明某 judge 的 LLM 需求。
type JudgeProfile struct {
	MaxTokens       int
	DisableThinking bool
}

// 每 judge 按任务复杂度自有 profile：
//   - 纯抽取（claim_coverage）关 thinking，省 token、根治空响应。
//   - 定性打分（reasoning_quality / insight_novelty）留 thinking，保判别质量；靠加大预算兜空响应。
var (
	claimCoverageProfile = JudgeProfile{MaxTokens: judgeMaxTokens, DisableThinking: true}
	scoringJudgeProfile  = JudgeProfile{MaxTokens: judgeMaxTokens, DisableThinking: false}
)

// tuneJudge 是唯一一处「按 profile 调教 client」的工厂。
// 只对实现 llm.JudgeProfiler 的真 client 生效；mock/stateless judge 原样透传。
func tuneJudge(base llm.Client, p JudgeProfile) llm.Client {
	if pr, ok := base.(llm.JudgeProfiler); ok {
		return pr.WithJudgeProfile(p.MaxTokens, p.DisableThinking)
	}
	return base
}
