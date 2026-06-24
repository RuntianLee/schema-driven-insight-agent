// framework/eval_harness/evaluators/answer_grounding.go
package evaluators

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/RuntianLee/schema-driven-insight-agent/contract"
	"github.com/RuntianLee/schema-driven-insight-agent/llm"
	"gopkg.in/yaml.v3"
)

// claimVerdict 是判官对单个定量主张的判定 + 结构化接地锚。
// Status 是判官软判（grounded|ungrounded）；Anchor/ClaimedValue 供确定性 resolver 复核。
type claimVerdict struct {
	Claim        string  `json:"claim"`
	Status       string  `json:"status"`
	Anchor       string  `json:"anchor"`        // 单元格路径 或 派生式（OpCatalog 语法）
	Kind         string  `json:"kind"`          // cell | derived
	ClaimedValue float64 `json:"claimed_value"` // 判官从主张读到的数值（与 anchor 量纲一致）
}

// answerGroundingReply 是判官输出的完整定量主张台账。
type answerGroundingReply struct {
	Score  int            `json:"score"`
	Claims []claimVerdict `json:"claims"`
	Reason string         `json:"reason"`
}

// AnswerGrounding 是 LLM judge（Deterministic=false，永不进 gate）：审 Analyst 自然语言
// 结论(res.Answer)里每个定量主张是否能被结构化结果(res.ToolCalls[].Response)接地。
// 复用 judgeSpec{Rubric,MinScore} 与 judgeMaxAttempts/markBelow（DRY）。
type AnswerGrounding struct {
	client llm.Client
}

func NewAnswerGrounding(client llm.Client) *AnswerGrounding { return &AnswerGrounding{client: client} }

func (a *AnswerGrounding) Name() string        { return "answer_grounding" }
func (a *AnswerGrounding) Deterministic() bool { return false }

func (a *AnswerGrounding) Evaluate(ctx context.Context, res TaskResult, spec *yaml.Node) (Score, error) {
	var sp judgeSpec
	if err := spec.Decode(&sp); err != nil {
		return Score{}, fmt.Errorf("decode answer_grounding spec: %w", err)
	}
	prompt := buildAnswerGroundingPrompt(sp.Rubric, res.Answer, res.ToolCalls)

	var raw string
	var callErr error
	for attempt := 1; attempt <= judgeMaxAttempts; attempt++ {
		raw, _, _, _, callErr = a.client.Call(ctx, prompt)
		if callErr == nil {
			break
		}
	}
	if callErr != nil {
		return Score{
			Evaluator: a.Name(), Value: 0, Pass: false, Errored: true, Display: "ERR",
			Detail: fmt.Sprintf("judge 调用失败（已重试 %d 次）: %v", judgeMaxAttempts-1, callErr),
		}, nil
	}

	reply, perr := parseAnswerGroundingReply(raw)
	if perr != nil {
		return Score{Evaluator: a.Name(), Value: 0, Pass: false, BelowMin: true,
			Display: "解析失败", Detail: fmt.Sprintf("judge 输出无效: %v（原文 %q）", perr, raw)}, nil
	}
	below := sp.MinScore > 0 && reply.Score < sp.MinScore
	verdicts := make([]AttributionVerdict, len(reply.Claims))
	for i, c := range reply.Claims {
		verdicts[i] = EvalAnchor(res.ToolCalls, c.Anchor, c.ClaimedValue, defaultAttrTol)
	}
	return Score{
		Evaluator: a.Name(),
		Value:     float64(reply.Score) / 5.0,
		Pass:      false, // judge 永不参与 gate
		BelowMin:  below,
		Display:   fmt.Sprintf("%d/5 %s", reply.Score, markBelow(below)),
		Detail:    renderLedger(reply, verdicts),
	}, nil
}

// renderLedger 渲染主张台账：每条带判官软判 + resolver 确定性裁决；附 attribution_resolved_rate 与 reason。
func renderLedger(r answerGroundingReply, verdicts []AttributionVerdict) string {
	var b strings.Builder
	rate := AttributionRate(verdicts)
	fmt.Fprintf(&b, "attribution_resolved_rate=%.2f（共 %d 条）。", rate, len(r.Claims))
	for i, c := range r.Claims {
		st := AttrUnresolvable
		if i < len(verdicts) {
			st = verdicts[i].Status
		}
		fmt.Fprintf(&b, " 「%s」judge=%s resolver=%s[%s];", c.Claim, c.Status, st, c.Anchor)
	}
	if r.Reason != "" {
		fmt.Fprintf(&b, " %s", r.Reason)
	}
	return b.String()
}

// 压缩器上限：控制喂给判官的 prompt 体量，防止大 Response 撞 LLM 超时（实证：
// 非流式下整段生成 >60s 即 "context deadline exceeded while awaiting headers"，
// 且超时与输入大小强相关）。砍臃肿数组、保留可被引用的聚合统计。
const (
	maxGroups  = 24 // 最多展开多少组 GroupProfile
	maxBuckets = 24 // 最多展开多少个分布 BucketRow
	maxRows    = 40 // 最多展开多少行 TableResult
)

// buildAnswerGroundingPrompt 把 narrative + 压缩序列化的 q1..qN（结构化结果）喂判官。
func buildAnswerGroundingPrompt(rubric, narrative string, calls []contract.ToolCall) string {
	var ev strings.Builder
	for i, tc := range calls {
		argsJSON, _ := json.Marshal(tc.Args)
		fmt.Fprintf(&ev, "%s: %s(%s) → %s\n", contract.AnalystResultID(i), tc.Name, argsJSON, compactResponse(tc.Response))
	}
	return fmt.Sprintf(`你是数据忠实度评审官。逐个检查"待评回答"里的每个定量主张（数字/阈值/比较/比例），是否能被下方结构化结果支撑。

评分准则（rubric）：%s

结构化结果（唯一真值来源，q1..qN）：
"""
%s"""

待评回答（Analyst 自然语言结论）：
"""
%s
"""

为每条定量主张给出**可机读接地锚** anchor：
- 直接单元格：q{N}.profile.<字段> / q{N}.group[键].profile.<字段> / q{N}.bucket[键].<字段> / q{N}.table.row[i].<列名> / q{N}.groups_tail.<字段>（字段名照结构化结果里出现的写）。
- 派生量：用算子包操作数路径，算子表：%s。例 ratio(q1.group[EU].profile.mean, q1.group[US].profile.mean)。
- 找不到出处则 anchor 留空字符串。
claimed_value 填你从该主张读到的数值，量纲与 anchor 单元格一致（占比用小数，如 42%%→0.42）。

只输出严格 JSON（不要解释、不要 markdown 代码块）：
{"score": <1-5 整数>, "claims": [{"claim":"<原文定量主张>","status":"grounded|ungrounded","anchor":"<路径或派生式或空>","kind":"cell|derived","claimed_value":<数值>}], "reason": "<一句话总评>"}`,
		rubric, ev.String(), narrative, OpCatalog())
}

// compactResponse 把 contract.Response 压成保留聚合统计、砍掉臃肿数组（TopN / 每组
// 嵌套 Data）的紧凑文本——保住答案可能引用的数字（count/各分位/mean/min/max、表格行），
// 大幅缩短喂判官的 prompt。
func compactResponse(r contract.Response) string {
	var b strings.Builder
	fmt.Fprintf(&b, "status=%s", r.Status)
	if r.Hint != "" {
		fmt.Fprintf(&b, " hint=%q", r.Hint)
	}
	if r.Profile != nil {
		fmt.Fprintf(&b, " profile{%s}", compactProfile(*r.Profile))
	}
	for i, g := range r.Groups {
		if i >= maxGroups {
			fmt.Fprintf(&b, " (+%d more groups)", len(r.Groups)-i)
			break
		}
		fmt.Fprintf(&b, " group[%s]{%s}", g.Group, compactProfile(g.Profile))
	}
	if r.GroupsTail != nil {
		fmt.Fprintf(&b, " groups_tail{groups=%d players=%d pct=%.3f}", r.GroupsTail.GroupCount, r.GroupsTail.PlayerCount, r.GroupsTail.PctPlayers)
	}
	for i, d := range r.Data {
		if i >= maxBuckets {
			fmt.Fprintf(&b, " (+%d more buckets)", len(r.Data)-i)
			break
		}
		fmt.Fprintf(&b, " bucket[%s]{n=%d pct=%.3f", d.Bucket, d.PlayerCount, d.PctPlayers)
		if d.AvgValue != 0 {
			fmt.Fprintf(&b, " avg=%.2f", d.AvgValue)
		}
		if d.TotalValue != 0 {
			fmt.Fprintf(&b, " total=%d", d.TotalValue)
		}
		b.WriteString("}")
	}
	if r.Table != nil {
		fmt.Fprintf(&b, " table{%s}", compactTable(*r.Table))
	}
	if len(r.Detail) > 0 {
		dj, _ := json.Marshal(r.Detail)
		fmt.Fprintf(&b, " detail=%s", dj)
	}
	return b.String()
}

// compactProfile 一行输出 DistProfile 的全部标量统计（保留所有分位，仅丢弃 TopN 数组）。
func compactProfile(p contract.DistProfile) string {
	return fmt.Sprintf("count=%d distinct=%d min=%.2f p10=%.2f p25=%.2f median=%.2f mean=%.2f p75=%.2f p90=%.2f p95=%.2f p99=%.2f max=%.2f tail_count=%d tail_pct=%.3f",
		p.Count, p.Distinct, p.Min, p.P10, p.P25, p.Median, p.Mean, p.P75, p.P90, p.P95, p.P99, p.Max, p.TailCount, p.TailPct)
}

// compactTable 输出列名 + 行数 + 截断到 maxRows 的原始行。
func compactTable(t contract.TableResult) string {
	cols := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		cols[i] = c.Name
	}
	var b strings.Builder
	fmt.Fprintf(&b, "cols=[%s] row_count=%d", strings.Join(cols, ","), t.RowCount)
	for i, row := range t.Rows {
		if i >= maxRows {
			fmt.Fprintf(&b, " (+%d more rows)", len(t.Rows)-i)
			break
		}
		rj, _ := json.Marshal(row)
		fmt.Fprintf(&b, " %s", rj)
	}
	return b.String()
}

var _ Evaluator = (*AnswerGrounding)(nil)

// parseAnswerGroundingReply 容错解析：从首个 { 起按单个 JSON 值解码（真 LLM 常包
// markdown fence / 前后缀 prose）；score 必须在 [1,5]，越界视为无效。
func parseAnswerGroundingReply(raw string) (answerGroundingReply, error) {
	start := strings.Index(raw, "{")
	if start < 0 {
		return answerGroundingReply{}, fmt.Errorf("非 JSON")
	}
	var reply answerGroundingReply
	if err := json.NewDecoder(strings.NewReader(raw[start:])).Decode(&reply); err != nil {
		return answerGroundingReply{}, fmt.Errorf("JSON 解析失败: %w", err)
	}
	if reply.Score < 1 || reply.Score > 5 {
		return answerGroundingReply{}, fmt.Errorf("score %d 越界（须 1-5）", reply.Score)
	}
	return reply, nil
}
