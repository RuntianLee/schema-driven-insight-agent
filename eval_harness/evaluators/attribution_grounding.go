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

func (a *AttributionGrounding) Evaluate(_ context.Context, res TaskResult, _ *yaml.Node) (Score, error) {
	if len(res.AttributionClaims) == 0 {
		return Score{
			Evaluator: a.Name(), Value: 1, Pass: true,
			Display: "skip（无归因块）",
		}, nil
	}
	bad := 0
	var badDetails []string
	for _, c := range res.AttributionClaims {
		v := EvalAnchor(res.ToolCalls, c.Anchor, c.ClaimedValue, defaultAttrTol)
		if v.Status == AttrMismatch || v.Status == AttrUnresolvable {
			bad++
			badDetails = append(badDetails, fmt.Sprintf("%s %s", c.Anchor, v.Status))
		}
	}
	n := len(res.AttributionClaims)
	pass := bad == 0
	val := 1.0
	if n > 0 {
		val = float64(n-bad) / float64(n)
	}
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
