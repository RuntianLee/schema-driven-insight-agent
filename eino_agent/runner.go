// Package eino_agent 是 agent.Runner 的 Eino 实现（Layer 2：手驱 ChatModel 循环 +
// 结构化 tool_use）。四道护栏（dedup/q-index/预算闸/maxTurns）为明文 Go；探测器链
// 降级为 provider 回退文本的 defense-in-depth fallback。trajectory 由 per-Run callback
// handler 接管（withTrajectory + recordedDispatch），业务循环零手工 Record*。
package eino_agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/RuntianLee/schema-driven-insight-agent/agent"
	"github.com/RuntianLee/schema-driven-insight-agent/contract"
	"github.com/RuntianLee/schema-driven-insight-agent/llm"
	"github.com/RuntianLee/schema-driven-insight-agent/prompts"
)

const (
	AgentVersion    = "v0.2.0" // Eino Layer 2 接管
	maxTurns        = 8
	maxBudgetTokens = 200_000
	maxBudgetUSD    = 1.0

	clarifyToolName = "request_clarification"
)

// Runner 是 Eino Layer 2 的 agent.Runner 实现。
type Runner struct {
	model         model.ToolCallingChatModel // 已绑 ToolInfos 的模型
	modelName     string
	tools         agent.ToolDispatcher
	trajDB        trajDBOpener
	schemaContext string
	clarifier     Clarifier
}

type trajDBOpener func(ctx context.Context, agentVersion, question string) (agent.TrajectoryStore, error)

// New 装配 Runner。传入的 model 在此绑定 ToolInfos（不可变 WithTools）；绑定失败即 panic（装配期错误）。
func New(m model.ToolCallingChatModel, modelName string, dispatcher agent.ToolDispatcher,
	opener trajDBOpener, schemaContext string, clarifier Clarifier) *Runner {
	bound, err := m.WithTools(ToolInfos())
	if err != nil {
		panic(fmt.Sprintf("eino_agent.New: WithTools: %v", err))
	}
	return &Runner{model: bound, modelName: modelName, tools: dispatcher,
		trajDB: opener, schemaContext: schemaContext, clarifier: clarifier}
}

func (r *Runner) Health(context.Context) error { return nil }
func (r *Runner) Stop(context.Context) error   { return nil }

// Run 执行一次任务循环：system+question → ChatModel → (tool_use → tool_result)* → 最终答案。
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
	ctx = withTrajectory(ctx, traj, r.modelName) // per-Run callback handler 接管 Record*

	msgs := []*schema.Message{
		schema.SystemMessage(buildSystemPrompt(prompts.SystemV0, r.schemaContext, time.Now())),
		schema.UserMessage(question),
	}

	seen := make(map[string]string) // canonicalKey → 上次结果 JSON
	okSeq := 0
	var spentTokens int
	var spentUSD float64

	for turn := 0; turn < maxTurns; turn++ {
		as, genErr := r.model.Generate(ctx, msgs) // callback → RecordLLMCall（真模型自动触发）
		var tokIn, tokOut int
		if as != nil && as.ResponseMeta != nil && as.ResponseMeta.Usage != nil {
			tokIn = as.ResponseMeta.Usage.PromptTokens
			tokOut = as.ResponseMeta.Usage.CompletionTokens
		}
		if genErr != nil {
			_ = traj.Finalize(ctx, "error", "", genErr.Error())
			return "", genErr
		}
		msgs = append(msgs, as)

		calls := structuredOrFallbackCalls(as)
		if len(calls) == 0 {
			_ = traj.Finalize(ctx, "success", as.Content, "")
			return as.Content, nil
		}

		// 预算闸只拦"还要继续循环"的路径。
		spentTokens += tokIn + tokOut
		spentUSD += llm.CostUSD(tokIn, tokOut)
		if spentTokens > maxBudgetTokens || spentUSD > maxBudgetUSD {
			msg := fmt.Sprintf("budget exceeded: tokens=%d (max %d) cost=$%.4f (max $%.2f)",
				spentTokens, maxBudgetTokens, spentUSD, maxBudgetUSD)
			_ = traj.Finalize(ctx, "error", "", msg)
			return "", fmt.Errorf("%s", msg)
		}

		// 逐 tool_use 派发（API 强制每个都配 tool_result）。
		for _, c := range calls {
			// request_clarification 拦截：转交 Clarifier，不进 dedup、不派发、不增 okSeq。
			if c.Function.Name == clarifyToolName {
				answer := r.clarifier.Ask(ctx, clarifyQuestion(c.Function.Arguments))
				msgs = append(msgs, schema.ToolMessage(answer, c.ID))
				continue
			}
			args := parseArgs(c.Function.Arguments)
			key := canonicalToolKey(toolCall{Tool: c.Function.Name, Args: args})
			if prior, dup := seen[key]; dup {
				msgs = append(msgs, schema.ToolMessage(
					fmt.Sprintf("## 重复查询已拦截\n你发起的查询与之前**完全相同**（参数未变），框架未重复执行。上次结果如下：\n%s\n\n请**直接基于该结果作答**；若需不同切面，请**改变参数**再查。", prior),
					c.ID))
				continue
			}
			toolResp, _ := recordedDispatch(ctx, r.tools, c.Function.Name, args) // callback → RecordToolCall
			resultJSON, _ := json.Marshal(toolResp)
			seen[key] = string(resultJSON)
			var content string
			if toolResp.Status == contract.StatusOK {
				okSeq++
				content = fmt.Sprintf("## 工具 %s 返回（结果 id: q%d，归因块引用此 id）\n%s\n\n请据此给出最终回答或修正后重试。", c.Function.Name, okSeq, string(resultJSON))
			} else {
				content = fmt.Sprintf("## 工具 %s 返回（本次未成功，不计入结果编号）\n%s\n\n请修正后重试。", c.Function.Name, string(resultJSON))
			}
			msgs = append(msgs, schema.ToolMessage(content, c.ID))
		}
	}

	_ = traj.Finalize(ctx, "error", "", "exceeded max turns")
	return "", fmt.Errorf("exceeded max turns (%d) without final answer", maxTurns)
}

// structuredOrFallbackCalls 取本轮工具调用：优先结构化 ToolCalls；为空时对 Content 跑探测器链
// （defense-in-depth：provider/网关回退纯文本工具调用时仍能识别）。
func structuredOrFallbackCalls(as *schema.Message) []schema.ToolCall {
	if len(as.ToolCalls) > 0 {
		return as.ToolCalls
	}
	if c, ok := parseToolCall(as.Content); ok {
		argsJSON, _ := json.Marshal(c.Args)
		return []schema.ToolCall{{ID: "fallback_" + c.Tool, Type: "function",
			Function: schema.FunctionCall{Name: c.Tool, Arguments: string(argsJSON)}}}
	}
	return nil
}

// parseArgs 把 tool_use 的 Arguments(JSON 串) 解析为 map[string]any 供 Dispatch/dedup。
func parseArgs(argsJSON string) map[string]any {
	args := map[string]any{}
	_ = json.Unmarshal([]byte(argsJSON), &args)
	return args
}

// clarifyQuestion 从 request_clarification 的 Arguments(JSON 串) 取 question 字段。
func clarifyQuestion(argsJSON string) string {
	var a struct {
		Question string `json:"question"`
	}
	_ = json.Unmarshal([]byte(argsJSON), &a)
	return a.Question
}

// serializeMessages 把对话消息列表序列化为 trajectory 存储串（结构化 content 序列化格式）。
func serializeMessages(msgs []*schema.Message) string {
	b, _ := json.Marshal(msgs)
	return string(b)
}

// serializeAssistant 把 assistant 消息（含 tool_calls）序列化为 trajectory response 串。
func serializeAssistant(m *schema.Message) string {
	if m == nil {
		return ""
	}
	if len(m.ToolCalls) > 0 {
		b, _ := json.Marshal(m.ToolCalls)
		return string(b)
	}
	return m.Content
}

var _ agent.Runner = (*Runner)(nil)
