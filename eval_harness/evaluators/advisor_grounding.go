// framework/eval_harness/evaluators/advisor_grounding.go
package evaluators

import (
	"context"
	"fmt"

	"github.com/RuntianLee/schema-driven-insight-agent/contract"
	"gopkg.in/yaml.v3"
)

// AdvisorGrounding 是确定性 evaluator：校验建议草案每条 SourceRef 可溯源到 Analyst 结果、
// 且条目数达标（"Advisor 0 处碰原始数据" + 流水线端到端不退化的机检证据）。进 CI gate。
type AdvisorGrounding struct{}

func NewAdvisorGrounding() *AdvisorGrounding { return &AdvisorGrounding{} }

func (a *AdvisorGrounding) Name() string        { return "advisor_grounding" }
func (a *AdvisorGrounding) Deterministic() bool { return true }

type agSpec struct {
	MinItems int `yaml:"min_items"` // 草案至少几条（0=不要求）
}

func (a *AdvisorGrounding) Evaluate(_ context.Context, res TaskResult, spec *yaml.Node) (Score, error) {
	var sp agSpec
	if spec != nil {
		if err := spec.Decode(&sp); err != nil {
			return Score{}, fmt.Errorf("decode advisor_grounding spec: %w", err)
		}
	}
	if res.Advisory == nil {
		return Score{
			Evaluator: a.Name(),
			Value:     0,
			Pass:      false,
			Display:   "0.00 ✗",
			Detail:    "无 AdvisoryDraft（流水线未跑 Advisor）",
		}, nil
	}
	// 合法 ID 集 = q1..qN（N = **成功** 工具调用数），口径同 trajcapture.Capture.AnalystResults
	// 与 attribution.Resolve（contract.OKCalls，2026-06-27 (b') 统一）。
	ok := contract.OKCalls(res.ToolCalls)
	valid := make(map[string]bool, len(ok))
	for i := range ok {
		valid[contract.AnalystResultID(i)] = true
	}
	n := len(res.Advisory.Items)
	bad := 0
	for _, it := range res.Advisory.Items {
		if !valid[it.SourceRef] {
			bad++
		}
	}
	val := 1.0
	if n > 0 {
		val = float64(n-bad) / float64(n)
	}
	pass := bad == 0 && n >= sp.MinItems
	display := fmt.Sprintf("%d/%d 溯源 %s", n-bad, n, passMark(pass))
	detail := ""
	if !pass {
		detail = fmt.Sprintf("%d 条 SourceRef 无法溯源；草案 %d 条（要求 ≥%d）", bad, n, sp.MinItems)
	}
	return Score{Evaluator: a.Name(), Value: val, Pass: pass, Display: display, Detail: detail}, nil
}

// passMark 返回通过/失败的 Unicode 符号，供 Display 字段使用。
func passMark(ok bool) string {
	if ok {
		return "✓"
	}
	return "✗"
}

var _ Evaluator = (*AdvisorGrounding)(nil)
