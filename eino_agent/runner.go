// Package eino_agent 是 agent.Runner 的 V0 实现。V0 手写 LLM↔tool 循环；
// V1 迁移到 Eino callback 接管（业务代码 0 侵入，trajectory-spec-v2 §7）。
package eino_agent

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"strings"
	"time"

	"github.com/RuntianLee/schema-driven-insight-agent/agent"
	"github.com/RuntianLee/schema-driven-insight-agent/contract"
	"github.com/RuntianLee/schema-driven-insight-agent/llm"
	"github.com/RuntianLee/schema-driven-insight-agent/prompts"
	"github.com/RuntianLee/schema-driven-insight-agent/tools"
	"github.com/RuntianLee/schema-driven-insight-agent/trajectory"
)

// 编译期断言：路线 A——trajectory.Recorder 必须满足 agent.TrajectoryStore（landing-map §0）。
var _ agent.TrajectoryStore = (trajectory.Recorder)(nil)

// 编译期断言：tools.Registry 必须满足 agent.ToolDispatcher。
var _ agent.ToolDispatcher = (*tools.Registry)(nil)

const (
	AgentVersion = "v0.1.2"
	maxTurns     = 8 // headroom for legitimate SCHEMA_ERROR self-correction (was 5)

	// 成本护栏（maxTurns 之外的第二道闸：步数少但单轮巨大的场景）。
	// 累计 token（in+out）或累计成本任一超限 → 终止本次 Run（outcome=error，可观测）。
	maxBudgetTokens = 200_000
	maxBudgetUSD    = 1.0
)

type toolCall struct {
	Tool string         `json:"tool"`
	Args map[string]any `json:"args"`
}

// Runner 是 V0 的 agent.Runner 实现。
type Runner struct {
	llm           llm.Client
	tools         agent.ToolDispatcher
	trajDB        trajDBOpener // 每个 Run 开一条新 trajectory
	schemaContext string       // 由 Schema.Digest() 生成的结构摘要，注入到对话首轮
}

// trajDBOpener 抽象 trajectory.New，便于测试注入。
type trajDBOpener func(ctx context.Context, agentVersion, question string) (agent.TrajectoryStore, error)

// New 装配 Runner。schemaContext 由 Schema.Digest() 生成；传空串则省略（向后兼容）。
func New(client llm.Client, dispatcher agent.ToolDispatcher, opener trajDBOpener, schemaContext string) *Runner {
	return &Runner{llm: client, tools: dispatcher, trajDB: opener, schemaContext: schemaContext}
}

func (r *Runner) Health(_ context.Context) error { return nil }
func (r *Runner) Stop(_ context.Context) error   { return nil }

// Run 执行一次任务循环：system+question → LLM → (tool call → result)* → final answer。
func (r *Runner) Run(ctx context.Context, question string) (finalAnswer string, runErr error) {
	traj, err := r.trajDB(ctx, AgentVersion, question)
	if err != nil {
		return "", err
	}
	defer func() {
		if rec := recover(); rec != nil {
			_ = traj.Finalize(ctx, "abort", "", fmt.Sprintf("panic: %v", rec))
			panic(rec)
		}
	}()

	conversation := buildPrompt(prompts.SystemV0, r.schemaContext, time.Now(), question)

	// 防空转硬护栏：记录已执行过的查询（规范化 key → 上次结果 JSON）。完全相同的查询
	// 不重复打 DB，改为注入上次结果 + 明确反馈，逼模型作答或换参（system prompt 的软
	// 约束之外的确定性兜底，避免低活跃数据上 LLM 误判 filter 失效而反复重发同一查询）。
	seen := make(map[string]string)

	// okSeq：成功(OK)结果的累计序号，注入给 agent 作归因块 q{N} 的「结果 id」——
	// agent 抄此 id 而非心算第几个查询（口径同 contract.OKCalls / resolver，2026-06-27 (b')）。
	okSeq := 0

	var spentTokens int
	var spentUSD float64
	for turn := 0; turn < maxTurns; turn++ {
		t0 := time.Now()
		resp, tokIn, tokOut, cost, llmErr := r.llm.Call(ctx, conversation)
		traj.RecordLLMCall(conversation, resp, r.llm.Model(), tokIn, tokOut, cost, t0, time.Now(), llmErr)
		if llmErr != nil {
			_ = traj.Finalize(ctx, "error", "", llmErr.Error())
			return "", llmErr
		}
		call, isToolCall := parseToolCall(resp)
		if !isToolCall {
			_ = traj.Finalize(ctx, "success", resp, "")
			return resp, nil
		}

		// 预算闸只拦"还要继续循环"的路径——当轮已给出最终回答则照常返回。
		spentTokens += tokIn + tokOut
		spentUSD += cost
		if spentTokens > maxBudgetTokens || spentUSD > maxBudgetUSD {
			msg := fmt.Sprintf("budget exceeded: tokens=%d (max %d) cost=$%.4f (max $%.2f)",
				spentTokens, maxBudgetTokens, spentUSD, maxBudgetUSD)
			_ = traj.Finalize(ctx, "error", "", msg)
			return "", fmt.Errorf("%s", msg)
		}

		key := canonicalToolKey(call)
		if prior, dup := seen[key]; dup {
			// 完全相同的查询（table/column/filter/group_by/bucket_key 均未变）已执行过：
			// 不再派发，注入上次结果 + 强反馈，让下一轮作答或换参。
			conversation += fmt.Sprintf("\n\n## 重复查询已拦截\n你发起的查询与之前**完全相同**（参数未变），框架未重复执行。上次结果如下：\n%s\n\n请**直接基于该结果作答**；若需不同切面，请**改变参数**再查。不要重复同一查询。", prior)
			continue
		}

		t1 := time.Now()
		toolResp, toolErr := r.tools.Dispatch(ctx, call.Tool, call.Args)
		traj.RecordToolCall(call.Tool, call.Args, toolResp, t1, time.Now(), toolErr)

		resultJSON, _ := json.Marshal(toolResp)
		seen[key] = string(resultJSON)
		if toolResp.Status == contract.StatusOK {
			okSeq++
			conversation += fmt.Sprintf("\n\n## 工具 %s 返回（结果 id: q%d，归因块引用此 id）\n%s\n\n请据此给出最终回答或修正后重试。", call.Tool, okSeq, string(resultJSON))
		} else {
			conversation += fmt.Sprintf("\n\n## 工具 %s 返回（本次未成功，不计入结果编号）\n%s\n\n请修正后重试。", call.Tool, string(resultJSON))
		}
	}

	_ = traj.Finalize(ctx, "error", "", "exceeded max turns")
	return "", fmt.Errorf("exceeded max turns (%d) without final answer", maxTurns)
}

// detectors 是工具调用格式探测器链，按结构签名特异性从高到低排列；
// 首个命中即返回。纯散文（无任何命中）→ 当最终答案。
// 设计=vLLM tool-parser 注册表的 Go 移植（per-format parser + auto-detect + 归一）。
var detectors = []func(string) (toolCall, bool){
	parseMinimaxXMLToolCall,  // 家族C：<invoke> XML（args-blob 既有 + 逐参数）
	parseTaggedJSONToolCall,  // 家族B：<tool_call>{json}</tool_call> / [TOOL_CALLS][{json}]
	parseOpenAIJSONToolCall,  // 家族A：{name, arguments/parameters/input}
	parseProjectJSONToolCall, // 家族A：{tool, args}（项目自有，既有路径原样保留）
}

// parseToolCall 依次尝试各格式探测器，把 LLM 文本输出解析为工具调用。
func parseToolCall(s string) (toolCall, bool) {
	trimmed := strings.TrimSpace(s)
	for _, d := range detectors {
		if c, ok := d(trimmed); ok {
			return c, true
		}
	}
	return toolCall{}, false
}

// parseProjectJSONToolCall 解析项目自有格式 {"tool":"X","args":{...}}（家族A）。
// 完全保留重构前的 JSON 解析逻辑：首个 { 起 Decoder 解单值（容忍尾部散文），
// 失败再补裸键引号重试。decodeToolCall 要求 tool 非空，故只认本格式、不与 OpenAI 抢。
func parseProjectJSONToolCall(s string) (toolCall, bool) {
	start := strings.Index(s, "{")
	if start < 0 {
		return toolCall{}, false
	}
	if c, ok := decodeToolCall(s[start:]); ok {
		return c, true
	}
	return decodeToolCall(quoteBareJSONKeys(s[start:]))
}

// 占位：家族B、家族A-OpenAI 探测器在后续任务实现，此处先始终返回 false 以编译通过。
func parseTaggedJSONToolCall(s string) (toolCall, bool) { return toolCall{}, false }
func parseOpenAIJSONToolCall(s string) (toolCall, bool) { return toolCall{}, false }

var minimaxXMLToolCallPattern = regexp.MustCompile(`(?s)<invoke\s+name=["']([^"']+)["'][^>]*>.*?<parameter\s+name=["']args["'][^>]*>(.*?)</parameter>`)

// invokeBlockRe 抓第一个 <invoke name="X">…</invoke> 块（含其内部 body）。
var invokeBlockRe = regexp.MustCompile(`(?s)<invoke\s+name=["']([^"']+)["'][^>]*>(.*?)</invoke>`)

// paramRe 抓 <parameter name="K">V</parameter>（逐参数形态）。
var paramRe = regexp.MustCompile(`(?s)<parameter\s+name=["']([^"']+)["'][^>]*>(.*?)</parameter>`)

// parseMinimaxXMLToolCall 解析 MiniMax 原生 XML 工具调用（家族C）。
// 先试既有 args-blob 形态（单个 name="args" 内含整块 JSON），不命中则逐参数兜底：
// 取第一个 <invoke>，把其所有 <parameter name="K">V</parameter> 聚成 args，
// 每个 V 按 JSON 解码（数组/对象/数字/bool），失败当字符串标量。
func parseMinimaxXMLToolCall(s string) (toolCall, bool) {
	// 1) args-blob 既有路径优先（零回归）。
	if m := minimaxXMLToolCallPattern.FindStringSubmatch(s); len(m) == 3 {
		argsText := strings.TrimSpace(html.UnescapeString(m[2]))
		if args, ok := decodeToolArgs(argsText); ok {
			return toolCall{Tool: m[1], Args: args}, true
		}
		if args, ok := decodeToolArgs(quoteBareJSONKeys(argsText)); ok {
			return toolCall{Tool: m[1], Args: args}, true
		}
		// args-blob 命中签名但 JSON 坏 → 继续逐参数兜底，不在此 return false。
	}

	// 2) 逐参数兜底：第一个 <invoke> + 其 <parameter>。
	ib := invokeBlockRe.FindStringSubmatch(s)
	if len(ib) != 3 {
		return toolCall{}, false
	}
	params := paramRe.FindAllStringSubmatch(ib[2], -1)
	if len(params) == 0 {
		return toolCall{}, false
	}
	args := make(map[string]any, len(params))
	for _, p := range params {
		args[p[1]] = decodeParamValue(strings.TrimSpace(html.UnescapeString(p[2])))
	}
	return toolCall{Tool: ib[1], Args: args}, true
}

// decodeParamValue 尝试把逐参数值按 JSON 解码（[]/{}/数字/bool/null），失败则当字符串标量。
func decodeParamValue(v string) any {
	var out any
	if json.NewDecoder(strings.NewReader(v)).Decode(&out) == nil {
		return out
	}
	return v
}

func decodeToolArgs(s string) (map[string]any, bool) {
	var args map[string]any
	dec := json.NewDecoder(strings.NewReader(s))
	if err := dec.Decode(&args); err != nil {
		return nil, false
	}
	return args, true
}

func decodeToolCall(s string) (toolCall, bool) {
	var c toolCall
	dec := json.NewDecoder(strings.NewReader(s))
	if err := dec.Decode(&c); err != nil {
		return toolCall{}, false
	}
	if c.Tool == "" {
		return toolCall{}, false
	}
	return c, true
}

var bareJSONKeyPattern = regexp.MustCompile(`([{\s,])([A-Za-z_][A-Za-z0-9_]*)\s*:`)

func quoteBareJSONKeys(s string) string {
	return bareJSONKeyPattern.ReplaceAllString(s, `$1"$2":`)
}

// canonicalToolKey 把 tool 调用规范化为去重 key：tool 名 + args 的 JSON。
// encoding/json 对 map 键按字母序稳定输出，故同一 (tool, args) 不论 LLM 输出时
// 键序如何，得到的 key 一致（嵌套 args 如 filter 同理）；用于检测"完全相同的查询"。
func canonicalToolKey(c toolCall) string {
	b, err := json.Marshal(c)
	if err != nil {
		return c.Tool // 退化：marshal 失败（极少见）时仅按 tool 名
	}
	return string(b)
}

// cutoffWindowsDays 是 prompt 里预算 cutoff 表覆盖的相对时间窗口。
// 模型自算 cutoff 偶有偏差（实测算成 9 天而非 3 天）；预算表让 Agent 抄、不要心算。
var cutoffWindowsDays = []int{1, 3, 7, 14, 30}

// buildPrompt 拼接喂给 LLM 的完整 prompt：
//
//	system_v0  +  schema 摘要  +  当前时间 + 预算 cutoff 表  +  运营问题
//
// 注入"今天"避免模型靠训练截止日期猜；预算 cutoff 表覆盖常用窗口（1/3/7/14/30 日），
// Agent 直接抄即可，不需要做减法。其他天数仍可按公式自算（精确公式给出）。
func buildPrompt(systemPrompt, schemaContext string, now time.Time, question string) string {
	var b strings.Builder
	b.WriteString(systemPrompt)
	if schemaContext != "" {
		b.WriteString("\n\n")
		b.WriteString(schemaContext)
	}
	nowUnix := now.Unix()
	fmt.Fprintf(&b, "\n\n## 当前时间\n今天是 %s（unix=%d）。\n", now.Format("2006-01-02"), nowUnix)
	b.WriteString("\n### 相对时间 cutoff 速查（“N 日未登录”/“N 日未活跃”等问题直接抄；不要心算）\n")
	for _, d := range cutoffWindowsDays {
		fmt.Fprintf(&b, "- %d 日：cutoff = %d\n", d, nowUnix-int64(d)*86400)
	}
	fmt.Fprintf(&b, "- 其他天数：cutoff = %d - N*86400（精确公式；上表覆盖外才用）\n", nowUnix)
	b.WriteString("\n## 运营问题\n")
	b.WriteString(question)
	return b.String()
}

// 确保 Runner 满足 agent.Runner。
var _ agent.Runner = (*Runner)(nil)

// _ 引用 contract 包，避免误删 import（Response 经 Dispatch 流转）。
var _ = contract.StatusOK
