// Package reflexion 实现跨试 Reflexion：agent 某任务失败后，用「自己的轨迹 + 二值成败」
// 蒸出一条过程经验，按 taskID 累积进临时内存；ContextFor 把累积经验注入后续 trial。
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

// Provider 是跨试 Reflexion 的有状态实现。
type Provider struct {
	reflectLLM llm.Client
	lessons    map[string][]string // taskID → 累积过程经验（临时内存）
}

// New 构造 Provider；reflectLLM 用于失败后蒸经验（真道复用 MiniMax）。
func New(reflectLLM llm.Client) *Provider {
	return &Provider{reflectLLM: reflectLLM, lessons: make(map[string][]string)}
}

// 编译期断言：实现两接缝。
var (
	_ runners.ReflectionProvider = (*Provider)(nil)
	_ runners.ReflectionObserver = (*Provider)(nil)
)

// ContextFor 返回该 taskID 已累积的经验前缀；无经验返回空（= 与 baseline 等价）。
func (p *Provider) ContextFor(_ context.Context, taskID, _ string) (string, error) {
	ls := p.lessons[taskID]
	if len(ls) == 0 {
		return "", nil
	}
	var b strings.Builder
	b.WriteString("过往尝试的经验教训（避免重复同样的过程失误）：")
	for _, l := range ls {
		b.WriteString("\n- ")
		b.WriteString(l)
	}
	return b.String(), nil
}

// Observe 仅在【未通过】时调 reflect LLM 蒸出 1-2 句过程经验并 append（通过则跳过，省成本）。
func (p *Provider) Observe(ctx context.Context, res evaluators.TaskResult, passed bool) error {
	if passed {
		return nil
	}
	out, _, _, _, err := p.reflectLLM.Call(ctx, buildReflectPrompt(res))
	if err != nil {
		return err
	}
	if lesson := strings.TrimSpace(out); lesson != "" {
		p.lessons[res.TaskID] = append(p.lessons[res.TaskID], lesson)
	}
	return nil
}

// Reset 清空累积经验，供每个独立 reflexion 序列冷起。
func (p *Provider) Reset() { p.lessons = make(map[string][]string) }

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
	return fmt.Sprintf(`你刚才尝试回答一个数据分析任务，但结果未通过校验。
任务问题：%s
你的工具调用与返回状态：%s
你给出的回答：%s

请用 1-2 句话指出你这次的【过程性失误】（例如：选错工具形态、漏了分位口径、过滤/分组/聚合参数写错、该两步走却一步直查），并给出下次的具体改法。
只说方法层面的教训，不要复述或猜测正确答案。`, res.Question, tc.String(), res.Answer)
}
