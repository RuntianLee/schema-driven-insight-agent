// framework/eval_harness/evaluators/claim_coverage.go
package evaluators

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/RuntianLee/schema-driven-insight-agent/llm"
	"gopkg.in/yaml.v3"
)

// ClaimCoverage 是 LLM off-gate evaluator：提取 Answer 中的定量主张并与
// Analyst 自产归因块比对，输出 coverage_rate 软信号（Pass 永远 false，不进 CI gate）。
type ClaimCoverage struct{ client llm.Client }

// NewClaimCoverage 注入 LLM client（经 tuneJudge 套纯抽取 profile：thinking off）。
func NewClaimCoverage(client llm.Client) *ClaimCoverage {
	return &ClaimCoverage{client: tuneJudge(client, claimCoverageProfile)}
}

func (c *ClaimCoverage) Name() string        { return "claim_coverage" }
func (c *ClaimCoverage) Deterministic() bool { return false }

// Evaluate 实现 Evaluator 接口：提取 Answer 中定量主张数，与声明数比对，返回 coverage_rate。
func (c *ClaimCoverage) Evaluate(ctx context.Context, res TaskResult, _ *yaml.Node) (Score, error) {
	// 任一为空 → skip（无法比对）
	if len(res.AttributionClaims) == 0 || res.Answer == "" {
		return Score{Evaluator: c.Name(), Value: 1, Pass: false, Display: "skip"}, nil
	}

	raw, _, _, _, err := c.client.Call(ctx, buildCoveragePrompt(res.Answer))
	if err != nil {
		return Score{
			Evaluator: c.Name(), Value: 0, Pass: false, Errored: true,
			Display: "ERR", Detail: fmt.Sprintf("claim_coverage LLM 失败: %v", err),
		}, nil
	}

	extracted, perr := parseCoverageReply(raw)
	if perr != nil {
		rawTrunc := []rune(raw)
		if len(rawTrunc) > 200 {
			rawTrunc = rawTrunc[:200]
		}
		return Score{
			Evaluator: c.Name(), Value: 0, Pass: false, BelowMin: true,
			Display: "解析失败", Detail: fmt.Sprintf("%v（原文 %q）", perr, string(rawTrunc)),
		}, nil
	}

	declared := len(res.AttributionClaims)
	total := len(extracted)
	rate := 1.0
	// declared > total：LLM 提取偏少不惩罚分析师，视同全覆盖（rate=1.0）。
	if total > 0 && declared < total {
		rate = float64(declared) / float64(total)
	}
	return Score{
		Evaluator: c.Name(),
		Value:     rate,
		Pass:      false, // 永不进 gate
		Display:   fmt.Sprintf("%.2f（声明%d/提取%d）", rate, declared, total),
	}, nil
}

// buildCoveragePrompt 构造极简提取 prompt：只问"有哪些数字主张"，不判断接地。
func buildCoveragePrompt(answer string) string {
	return fmt.Sprintf(`列出下方文字里所有的定量主张（数字/百分比/倍数/比较结论）。
不含序号、时间、组别编号（如「第3组」「2023年」「前5名」）。
只输出 JSON 数组，不含 markdown：["主张1","主张2",...]

文字：
"""
%s
"""`, answer)
}

// parseCoverageReply 从首个 [ 起容错解码 JSON 数组。
func parseCoverageReply(raw string) ([]string, error) {
	start := strings.Index(raw, "[")
	if start < 0 {
		return nil, fmt.Errorf("非 JSON 数组（无 [）")
	}
	var out []string
	if err := json.NewDecoder(strings.NewReader(raw[start:])).Decode(&out); err != nil {
		return nil, fmt.Errorf("JSON 解析失败: %w", err)
	}
	return out, nil
}

var _ Evaluator = (*ClaimCoverage)(nil)
