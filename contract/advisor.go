package contract

import "strconv"

// ToolCall 是一次工具调用的捕获快照（统一类型；原 evaluators.ToolCall 迁移至此）。
type ToolCall struct {
	Name     string         `json:"name"`
	Args     map[string]any `json:"args,omitempty"`
	Response Response       `json:"response"`
	Err      error          `json:"-"`
}

// AnalystResult 是 Analyst 单次工具结果 + 稳定引用锚（供 Recommendation.SourceRef 指向）。
type AnalystResult struct {
	ID   string   `json:"id"`
	Call ToolCall `json:"call"`
}

// AnalystOutput 是 Analyst 的结构化产物——Advisor 的唯一入参（A3.4：不碰原始数据）。
type AnalystOutput struct {
	Question  string          `json:"question"`
	Results   []AnalystResult `json:"results"`
	Narrative string          `json:"narrative"`
}

// Recommendation 是一条建议草案（域中立命名，无 player/游戏词汇）。
type Recommendation struct {
	Observation string `json:"observation"`
	SourceRef   string `json:"source_ref"`
	Action      string `json:"action"`
	Priority    string `json:"priority"`
	Caveat      string `json:"caveat"`
}

// AdvisoryDraft 是 Advisor 的产物：一组可溯源建议草案 + 概述。
type AdvisoryDraft struct {
	Items   []Recommendation `json:"items"`
	Summary string           `json:"summary"`
}

// AnalystResultID 是第 i 个（0-based）Analyst 结果的稳定引用锚。
// trajcapture.Capture.AnalystResults 与 advisor_grounding evaluator 共用此口径（DRY）。
func AnalystResultID(i int) string { return "q" + strconv.Itoa(i+1) }

// OKCalls 过滤出 status=OK 的工具结果，供 q{N} 编号（失败/重试不计数，编号对重试鲁棒）。
// 单一真值源：trajcapture.AnalystResults、advisor_grounding、attribution.Resolve 共用，
// 杜绝「q2 指哪个」的口径分叉（2026-06-27 (b') 诊断）。
func OKCalls(calls []ToolCall) []ToolCall {
	out := make([]ToolCall, 0, len(calls))
	for _, c := range calls {
		if c.Response.Status == StatusOK {
			out = append(out, c)
		}
	}
	return out
}
