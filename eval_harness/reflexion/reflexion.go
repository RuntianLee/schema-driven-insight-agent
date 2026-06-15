// Package reflexion 实现分域 Reflexion（Scoped Reflexion）：
//   - 查询正确（data_correctness 通过）但解读弱（reasoning_quality 未过）→
//     refine-explanation 模式：冻结正确查询，只精修解读，零额外 LLM 调用；
//   - 查询错误（data_correctness 未过）→ fix-query 模式：自我批判蒸出过程经验，修查询；
//   - 全部通过 → 无需反思。
//
// 实现 runners.ReflectionProvider（只读注入）+ runners.ReflectionObserver（失败回写）。
// 临时内存：每个独立 reflexion 序列开始前由 A/B 编排器调 Reset 冷起（A/B 干净）。
package reflexion

import (
	"context"
	"fmt"
	"strings"

	"github.com/RuntianLee/schema-driven-insight-agent/eval_harness/evaluators"
	"github.com/RuntianLee/schema-driven-insight-agent/eval_harness/runners"
	"github.com/RuntianLee/schema-driven-insight-agent/llm"
)

// refineHint：上次查询正确、仅解读弱时的提示——复用原查询、只改进解读。
type refineHint struct {
	queryCalls []evaluators.ToolCall // 上次【正确】的工具调用（原样复用，避免二次查询出错）
	feedback   string                // reasoning judge 指出的解读不足
}

// Provider 是分域 Reflexion 的有状态实现。
type Provider struct {
	reflectLLM llm.Client
	lessons    map[string][]string   // fix-query 模式：过程经验（临时内存）
	refine     map[string]refineHint // refine-explanation 模式：冻结正确查询 + 改进解读
}

// New 构造 Provider；reflectLLM 用于查询失败后蒸经验（真道复用 MiniMax）。
func New(reflectLLM llm.Client) *Provider {
	return &Provider{reflectLLM: reflectLLM, lessons: make(map[string][]string), refine: make(map[string]refineHint)}
}

// 编译期断言：实现两接缝。
var (
	_ runners.ReflectionProvider = (*Provider)(nil)
	_ runners.ReflectionObserver = (*Provider)(nil)
)

// ContextFor 优先走 refine-explanation（查询已对，只改进解读）；否则走 fix-query 经验；都没有则空。
func (p *Provider) ContextFor(_ context.Context, taskID, _ string) (string, error) {
	if h, ok := p.refine[taskID]; ok {
		return buildRefineContext(h), nil
	}
	if ls := p.lessons[taskID]; len(ls) > 0 {
		var b strings.Builder
		b.WriteString("过往尝试的经验教训（避免重复同样的过程失误）：")
		for _, l := range ls {
			b.WriteString("\n- ")
			b.WriteString(l)
		}
		return b.String(), nil
	}
	return "", nil
}

// Observe 据本任务裁决分流：
//   - data_correctness 通过、reasoning_quality 未过 → refine-explanation（冻结正确查询，只改进解读，零额外 LLM 调用）；
//   - data_correctness 未过 → fix-query（自我批判蒸经验，修查询）；
//   - 全过 → 无需反思。
func (p *Provider) Observe(ctx context.Context, res evaluators.TaskResult, scores map[string]evaluators.Score) error {
	dc, hasDC := scores["data_correctness"]
	rq, hasRQ := scores["reasoning_quality"]
	switch {
	case hasDC && dc.Pass && hasRQ && !rq.Pass:
		p.refine[res.TaskID] = refineHint{queryCalls: res.ToolCalls, feedback: rq.Detail}
	case hasDC && !dc.Pass:
		out, _, _, _, err := p.reflectLLM.Call(ctx, buildReflectPrompt(res))
		if err != nil {
			return err
		}
		if l := strings.TrimSpace(out); l != "" {
			p.lessons[res.TaskID] = append(p.lessons[res.TaskID], l)
		}
	}
	return nil
}

// Reset 清空所有状态，供每个独立 reflexion 序列冷起。
func (p *Provider) Reset() {
	p.lessons = make(map[string][]string)
	p.refine = make(map[string]refineHint)
}

// buildRefineContext 注入「复用正确查询 + 只改进解读」的指引，绝不改动查询口径、不编造数值。
func buildRefineContext(h refineHint) string {
	var b strings.Builder
	b.WriteString("你上次对这个问题的【分析查询是正确的】。请用完全相同的工具和参数再执行一次查询，不要改动查询本身；然后把对结果的【解读】写得更完整、更有运营价值。")
	if len(h.queryCalls) > 0 {
		b.WriteString("\n上次的正确查询：")
		for _, c := range h.queryCalls {
			fmt.Fprintf(&b, "\n- 工具 %s，参数 %v", c.Name, c.Args)
		}
	}
	if strings.TrimSpace(h.feedback) != "" {
		b.WriteString("\n上次解读被评审指出的不足：" + h.feedback)
	}
	b.WriteString("\n本次请补足：关键结论（如集中度/头部效应）、量化对比、针对性运营建议、数据口径或局限 caveat。不要改动查询口径，也不要编造数值。")
	return b.String()
}

// buildReflectPrompt 用 agent 自己的轨迹 + 「未通过」构造反思 prompt。
// 只引用 agent 自身调用与状态码，绝不接触 evaluator 的 golden 期望表。
func buildReflectPrompt(res evaluators.TaskResult) string {
	var tc strings.Builder
	for _, c := range res.ToolCalls {
		fmt.Fprintf(&tc, "\n- 工具 %s, 参数 %v, 返回状态 %s", c.Name, c.Args, c.Response.Status)
	}
	if tc.Len() == 0 {
		tc.WriteString("（无工具调用）")
	}
	return fmt.Sprintf(`你刚才尝试回答一个数据分析任务，但本次结果未达标（要么查询口径不对，要么对结果的解读/运营洞察被评为偏弱）。
任务问题：%s
你的工具调用与返回状态：%s
你给出的回答：%s

请用 1-2 句话指出本次的具体不足并给出下次改进：
- 若是查询层面：选错工具形态 / 漏了过滤或分位口径 / 过滤/分组/聚合参数写错；
- 若是解读层面：未点出关键结论（如集中度/头部效应）、未给运营含义或建议、未提数据口径 caveat。
只说方法与表达层面的改进，不要复述或编造具体数值答案。`, res.Question, tc.String(), res.Answer)
}
