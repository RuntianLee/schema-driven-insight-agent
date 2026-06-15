// framework/eval_harness/evaluators/judge.go
package evaluators

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/RuntianLee/schema-driven-insight-agent/llm"
	"gopkg.in/yaml.v3"
)

type judgeSpec struct {
	Rubric   string `yaml:"rubric"`
	MinScore int    `yaml:"min_score"`
}

type judgeReply struct {
	Score  int    `json:"score"`
	Reason string `json:"reason"`
}

// judgeEvaluator 是 reasoning_quality / insight_novelty 的共享基底（spec §4.2-4.3）。
// 真构造 prompt + 真解析；client 是 mock 或真 LLM（唯一切换点）。Deterministic 恒 false。
type judgeEvaluator struct {
	name   string
	intro  string // judge 角色说明（区分两个 judge 的视角）
	client llm.Client
}

func (j *judgeEvaluator) Name() string        { return j.name }
func (j *judgeEvaluator) Deterministic() bool { return false }

func (j *judgeEvaluator) Evaluate(ctx context.Context, res TaskResult, spec *yaml.Node) (Score, error) {
	var sp judgeSpec
	if err := spec.Decode(&sp); err != nil {
		return Score{}, fmt.Errorf("decode %s spec: %w", j.name, err)
	}
	prompt := j.buildPrompt(sp.Rubric, res.Answer)
	raw, _, _, _, err := j.client.Call(ctx, prompt)
	if err != nil {
		return Score{}, fmt.Errorf("%s judge call: %w", j.name, err)
	}
	reply, perr := parseJudgeReply(raw)
	if perr != nil {
		// R3：解析失败不中断 suite，记 0 分 + 原文；视为未达标允许 refine 尝试改进。
		return Score{Evaluator: j.name, Value: 0, Pass: false, BelowMin: true,
			Display: "解析失败", Detail: fmt.Sprintf("judge 输出无效: %v（原文 %q）", perr, raw)}, nil
	}
	below := sp.MinScore > 0 && reply.Score < sp.MinScore
	return Score{
		Evaluator: j.name,
		Value:     float64(reply.Score) / 5.0,
		Pass:      false, // judge 永不参与 gate
		BelowMin:  below,
		Display:   fmt.Sprintf("%d/5 %s", reply.Score, markBelow(below)),
		Detail:    reply.Reason,
	}, nil
}

// parseJudgeReply 容错解析 judge 输出：真 LLM 常包 markdown fence 或前后缀 prose，
// 从首个 { 起按单个 JSON 值解码；score 必须在 [1,5]（prompt 约定），越界视为无效
// （防 Value 越界污染报告，如 score=7 → 1.4）。
func parseJudgeReply(raw string) (judgeReply, error) {
	start := strings.Index(raw, "{")
	if start < 0 {
		return judgeReply{}, fmt.Errorf("非 JSON")
	}
	var reply judgeReply
	if err := json.NewDecoder(strings.NewReader(raw[start:])).Decode(&reply); err != nil {
		return judgeReply{}, fmt.Errorf("JSON 解析失败: %w", err)
	}
	if reply.Score < 1 || reply.Score > 5 {
		return judgeReply{}, fmt.Errorf("score %d 越界（须 1-5）", reply.Score)
	}
	return reply, nil
}

func markBelow(below bool) string {
	if below {
		return "⚠below-min"
	}
	return ""
}

func (j *judgeEvaluator) buildPrompt(rubric, answer string) string {
	return fmt.Sprintf(`%s

评分准则（rubric）：%s

待评 Agent 回答：
"""
%s
"""

只输出严格 JSON（不要解释、不要 markdown 代码块）：{"score": <1-5 整数>, "reason": "<一句话>"}`,
		j.intro, rubric, answer)
}
