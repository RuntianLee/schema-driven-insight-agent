// Package eino_agent 是 agent.Runner 的 V0 实现。V0 手写 LLM↔tool 循环；
// V1 迁移到 Eino callback 接管（业务代码 0 侵入，trajectory-spec-v2 §7）。
package eino_agent

import (
	"context"
	"encoding/json"
	"fmt"
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

// 确保 Runner 满足 agent.Runner。
var _ agent.Runner = (*Runner)(nil)

// _ 引用 contract 包，避免误删 import（Response 经 Dispatch 流转）。
var _ = contract.StatusOK
