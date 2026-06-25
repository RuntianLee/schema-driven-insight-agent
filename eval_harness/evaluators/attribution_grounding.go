// framework/eval_harness/evaluators/attribution_grounding.go
package evaluators

import (
	"context"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// AttributionGrounding 是确定性 evaluator：校验 Analyst 自产归因块里每条
// ClaimAnchor 可溯源且与单元格值匹配（bad > 0 → gate 失败）。进 CI gate。
// AttrDerivUnsupported 宽容：算子注册表欠债，非幻觉，不计 bad。
type AttributionGrounding struct{}

func NewAttributionGrounding() *AttributionGrounding { return &AttributionGrounding{} }

func (a *AttributionGrounding) Name() string        { return "attribution_grounding" }
func (a *AttributionGrounding) Deterministic() bool { return true }

type attrGroundingSpec struct {
	MinClaims int `yaml:"min_claims"` // 至少几条归因主张（0=不要求，缺块 skip；≥1=缺块/不足→FAIL）
}

func (a *AttributionGrounding) Evaluate(_ context.Context, res TaskResult, spec *yaml.Node) (Score, error) {
	var sp attrGroundingSpec
	if spec != nil {
		if err := spec.Decode(&sp); err != nil {
			return Score{}, fmt.Errorf("decode attribution_grounding spec: %w", err)
		}
	}
	n := len(res.AttributionClaims)
	if n < sp.MinClaims {
		// 要求产块却不足 → FAIL（治理缺块 skip-PASS，2026-06-26 T2 实测 40%）。
		return Score{
			Evaluator: a.Name(), Value: 0, Pass: false,
			Display: fmt.Sprintf("%d/%d ✗（要求≥%d 条归因块）", n, sp.MinClaims, sp.MinClaims),
			Detail:  "应产归因块的量化任务未产块/不足——缺块不再 skip-pass",
		}, nil
	}
	if n == 0 {
		// 无要求（min_claims=0）且无块 → skip（向后兼容，默认行为零改动）。
		return Score{Evaluator: a.Name(), Value: 1, Pass: true, Display: "skip（无归因块）"}, nil
	}
	bad := 0
	var badDetails []string
	for _, c := range res.AttributionClaims {
		v := EvalAnchor(res.ToolCalls, c.Anchor, c.ClaimedValue, defaultAttrTol)
		if v.Status == AttrMismatch || v.Status == AttrUnresolvable {
			bad++
			badDetails = append(badDetails, fmt.Sprintf("「%s」anchor=%q %s", c.Claim, c.Anchor, v.Status))
		}
	}
	pass := bad == 0
	val := float64(n-bad) / float64(n) // n≥1 此处保证（n==0 已上方返回）
	var display string
	if pass {
		display = fmt.Sprintf("%d/%d ✓", n, n)
	} else {
		display = fmt.Sprintf("%d/%d ✗ (%d mismatch/unresolvable)", n-bad, n, bad)
	}
	score := Score{Evaluator: a.Name(), Value: val, Pass: pass, Display: display}
	if !pass {
		score.Detail = strings.Join(badDetails, "; ")
	}
	return score, nil
}

var _ Evaluator = (*AttributionGrounding)(nil)
