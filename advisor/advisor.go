// Package advisor 是 Advisor 建议草案 agent（V2 子项 #2）。
// 唯一入参是 contract.AnalystOutput——结构上拿不到 DB / 工具（A3.4：不碰原始数据）。
package advisor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/RuntianLee/schema-driven-insight-agent/contract"
	"github.com/RuntianLee/schema-driven-insight-agent/llm"
)

// Advisor 消费 Analyst 结构化输出 + adapter playbook，产出可溯源建议草案。
type Advisor struct {
	llm          llm.Client
	systemPrompt string
}

// New 创建 Advisor 实例。
func New(client llm.Client, systemPrompt string) *Advisor {
	return &Advisor{llm: client, systemPrompt: systemPrompt}
}

// Advise：拼 prompt → LLM → 解析草案 → 确定性自校验（丢弃幻觉 SourceRef）。
func (a *Advisor) Advise(ctx context.Context, out contract.AnalystOutput, playbook string) (contract.AdvisoryDraft, error) {
	prompt, err := buildPrompt(a.systemPrompt, out, playbook)
	if err != nil {
		return contract.AdvisoryDraft{}, err
	}
	resp, _, _, _, err := a.llm.Call(ctx, prompt)
	if err != nil {
		return contract.AdvisoryDraft{}, fmt.Errorf("advisor llm call: %w", err)
	}
	draft, err := parseDraft(resp)
	if err != nil {
		return contract.AdvisoryDraft{}, err
	}
	return validate(draft, out), nil
}

func buildPrompt(systemPrompt string, out contract.AnalystOutput, playbook string) (string, error) {
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return "", fmt.Errorf("advisor marshal analyst output: %w", err)
	}
	var sb strings.Builder
	sb.WriteString(systemPrompt)
	if strings.TrimSpace(playbook) != "" {
		sb.WriteString("\n\n## 运营 playbook（adapter 提供）\n")
		sb.WriteString(playbook)
	}
	sb.WriteString("\n\n## Analyst 结构化输出（你唯一的依据）\n")
	sb.Write(b)
	return sb.String(), nil
}

// parseDraft 取响应里首个 JSON 对象解析为 AdvisoryDraft（容忍尾部多余文字，复用 runner 同款宽松度）。
func parseDraft(s string) (contract.AdvisoryDraft, error) {
	start := strings.Index(s, "{")
	if start < 0 {
		return contract.AdvisoryDraft{}, fmt.Errorf("advisor: 响应无 JSON 对象")
	}
	var d contract.AdvisoryDraft
	dec := json.NewDecoder(strings.NewReader(s[start:]))
	if err := dec.Decode(&d); err != nil {
		return contract.AdvisoryDraft{}, fmt.Errorf("advisor: 草案 JSON 解析失败: %w", err)
	}
	return d, nil
}

// validate 丢弃 SourceRef 不在 out.Results 内的条目（确定性自校验，防幻觉引用）。
func validate(d contract.AdvisoryDraft, out contract.AnalystOutput) contract.AdvisoryDraft {
	ids := make(map[string]bool, len(out.Results))
	for _, r := range out.Results {
		ids[r.ID] = true
	}
	kept := make([]contract.Recommendation, 0, len(d.Items))
	for _, it := range d.Items {
		if ids[it.SourceRef] {
			kept = append(kept, it)
		}
	}
	d.Items = kept
	return d
}
