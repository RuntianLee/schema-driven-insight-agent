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

// claimVerdict 是判官对单个定量主张的判定 + 证据锚点。
type claimVerdict struct {
	Claim    string `json:"claim"`
	Status   string `json:"status"` // "grounded" | "ungrounded"
	Evidence string `json:"evidence"`
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
	return Score{
		Evaluator: a.Name(),
		Value:     float64(reply.Score) / 5.0,
		Pass:      false, // judge 永不参与 gate
		BelowMin:  below,
		Display:   fmt.Sprintf("%d/5 %s", reply.Score, markBelow(below)),
		Detail:    renderLedger(reply),
	}, nil
}

// renderLedger 渲染完整定量主张台账：先列未接地（疑似幻觉），再附 reason。
func renderLedger(r answerGroundingReply) string {
	var b strings.Builder
	var ungrounded []string
	for _, c := range r.Claims {
		if c.Status == "ungrounded" {
			ungrounded = append(ungrounded, fmt.Sprintf("「%s」(%s)", c.Claim, c.Evidence))
		}
	}
	if len(ungrounded) > 0 {
		b.WriteString("未接地: " + strings.Join(ungrounded, "; ") + "。")
	}
	fmt.Fprintf(&b, "主张共 %d 条。%s", len(r.Claims), r.Reason)
	return b.String()
}

// buildAnswerGroundingPrompt 把 narrative + 紧凑序列化的 q1..qN（结构化结果）喂判官。
func buildAnswerGroundingPrompt(rubric, narrative string, calls []contract.ToolCall) string {
	var ev strings.Builder
	for i, tc := range calls {
		argsJSON, _ := json.Marshal(tc.Args)
		respJSON, _ := json.Marshal(tc.Response)
		fmt.Fprintf(&ev, "%s: %s(%s) → %s\n", contract.AnalystResultID(i), tc.Name, argsJSON, respJSON)
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

只输出严格 JSON（不要解释、不要 markdown 代码块）：
{"score": <1-5 整数>, "claims": [{"claim":"<原文里的定量主张>","status":"grounded|ungrounded","evidence":"<接地的 qN.字段/派生式，或未接地说明>"}], "reason": "<一句话总评>"}`,
		rubric, ev.String(), narrative)
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
