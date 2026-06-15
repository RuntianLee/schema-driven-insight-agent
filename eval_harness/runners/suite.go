// framework/eval_harness/runners/suite.go
// Package runners 提供 eval harness 的编排层：sequencedMock + captureStore + RunSuite。
package runners

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/RuntianLee/schema-driven-insight-agent/agent"
	"github.com/RuntianLee/schema-driven-insight-agent/eino_agent"
	evalpkg "github.com/RuntianLee/schema-driven-insight-agent/eval_harness"
	"github.com/RuntianLee/schema-driven-insight-agent/eval_harness/evaluators"
	"github.com/RuntianLee/schema-driven-insight-agent/llm"
	"github.com/RuntianLee/schema-driven-insight-agent/trajectory"
	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

// TaskInput 是 RunSuite 的任务输入（与 tasks.Task 同形，解耦 import 方向）。
// 导出：cmd 跨包构造（Task 12）。
type TaskInput struct {
	ID         string
	Title      string
	Question   string
	LLMTurns   []string
	Evaluators map[string]yaml.Node
}

// Config 装配 RunSuite 的全部依赖（adapter-agnostic）。
type Config struct {
	Dispatcher agent.ToolDispatcher // 真 tools.Registry 连 fixture
	SchemaCtx  string               // schema.Digest()
	EvalReg    *evaluators.Registry // 已注入 judge client 的 evaluator 集
	EvalOrder  []string             // 报告列顺序（也定 gate 范围）
	Tasks      []TaskInput
	TrajDB     *sql.DB // 非 nil 则把每任务完整轨迹 + verdict 落库（task_class=benchmark）；nil 退回纯内存
	// AgentLLM 非 nil 则 agent 用它推理（真评测道），task.LLMTurns 被忽略；
	// nil 则退回 sequencedMock（确定性 mock 道，CI 默认）。
	AgentLLM llm.Client
	// ReflectionProvider 非 nil 则在 agent 跑每个任务前，把其返回的上下文前置注入 question
	// （reflection 开，A/B 的 config B）；nil（默认）= reflection 关（config A / 既有行为）。
	ReflectionProvider ReflectionProvider
}

// RunSuite 逐任务跑 agent（mock LLM）+ 真 tool，收集 TaskResult，跑 evaluator，汇总 Report。
func RunSuite(ctx context.Context, cfg Config) (*evalpkg.Report, error) {
	rep := evalpkg.NewReport(cfg.EvalOrder)
	for _, task := range cfg.Tasks {
		var agentClient llm.Client = cfg.AgentLLM
		if agentClient == nil {
			agentClient = newSequencedMock(task.LLMTurns)
		}
		capture := newCaptureStore()
		var store agent.TrajectoryStore = capture
		var trajID string
		if cfg.TrajDB != nil {
			if rec, err := trajectory.New(ctx, cfg.TrajDB, eino_agent.AgentVersion, task.Question, "benchmark"); err == nil {
				tee := newTeeStore(capture, rec)
				store = tee
				trajID = tee.TrajectoryID()
			}
			// New 失败：仅退回纯内存（不影响评测），与"永不干扰主流程"一致。
		}
		opener := func(_ context.Context, _, _ string) (agent.TrajectoryStore, error) {
			return store, nil
		}
		runner := eino_agent.New(agentClient, cfg.Dispatcher, opener, cfg.SchemaCtx)
		runQuestion := applyReflection(ctx, cfg.ReflectionProvider, task.ID, task.Question)
		answer, runErr := runner.Run(ctx, runQuestion)

		res := evaluators.TaskResult{
			TaskID:    task.ID,
			Question:  task.Question,
			Answer:    answer,
			Outcome:   capture.outcome,
			ToolCalls: capture.toolCalls,
			RunErr:    runErr,
		}

		var dcPassed bool
		for _, name := range cfg.EvalOrder {
			spec, hasSpec := task.Evaluators[name]
			if !hasSpec {
				continue
			}
			e, ok := cfg.EvalReg.Get(name)
			if !ok {
				// 任务选了 evaluator 却没注册 → 配置 bug，loud 失败而非静默漏列。
				return nil, fmt.Errorf("任务 %q 选用 evaluator %q，但未在 EvalReg 注册（配置错误）", task.ID, name)
			}
			specCopy := spec
			score, err := e.Evaluate(ctx, res, &specCopy)
			if err != nil {
				score = evaluators.Score{Evaluator: name, Value: 0, Pass: false,
					Display: "ERR", Detail: err.Error()}
			}
			rep.Add(task.ID, score, e.Deterministic())
			if name == "data_correctness" {
				dcPassed = score.Pass
			}
			if cfg.TrajDB != nil && trajID != "" {
				persistVerdict(cfg.TrajDB, trajID, task.ID, score)
			}
		}
		// reflection 回写接缝（#4）：provider 实现 ReflectionObserver 则喂自身轨迹 + 二值成败。
		// 失败仅吞——同 persistVerdict，绝不干扰评测主流程。
		// nil / 非-Observer provider 不触发（确定性 gate 字节不变）。
		if obs, ok := cfg.ReflectionProvider.(ReflectionObserver); ok {
			_ = obs.Observe(ctx, res, dcPassed)
		}
	}
	return rep, nil
}

// applyReflection 在 provider 非 nil 且返回非空时，把 reflection 上下文前置到 question。
// eino_agent 零改动：reflection 作为额外上下文随 question 进入 agent 本轮对话。
// #4 可改注入点（如 schemaContext / 专用记忆轮），本接缝对 A/B 编排足够。
func applyReflection(ctx context.Context, p ReflectionProvider, taskID, question string) string {
	if p == nil {
		return question
	}
	rc, err := p.ContextFor(ctx, taskID, question)
	if err != nil || rc == "" {
		return question
	}
	return rc + "\n\n---\n\n" + question
}

// persistVerdict 把一个 evaluator 评分写入 eval_results（与 trajectory 关联）。
// 失败仅吞——eval gate 退出码只由 evaluator 分数决定，落库不得干扰主流程。
func persistVerdict(db *sql.DB, trajID, taskID string, s evaluators.Score) {
	pass := 0
	if s.Pass {
		pass = 1
	}
	_, _ = db.Exec(
		`INSERT INTO eval_results (result_id, trajectory_id, task_id, evaluator_name, value, pass, display, created_at)
		 VALUES (?,?,?,?,?,?,?,?)`,
		uuid.NewString(), trajID, taskID, s.Evaluator, s.Value, pass, s.Display, time.Now().Unix())
}
