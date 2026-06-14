// Package evalcli 是 adapter eval 命令的共享装配层：每任务独立 fixture 的评测形态
// （区别于 cmd/eval 的"单库 + 任务目录"形态）。adapter 的 cmd/eval 只需提供
// fixture 映射与默认路径，主体（schema 解析、任务装载、registry 装配、真/mock 道、
// trajectory 落库、报告/history 落盘、gate 退出码）全部在此复用——避免多 adapter
// 间 95% 复制漂移。
package evalcli

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/RuntianLee/schema-driven-insight-agent/contract"
	"github.com/RuntianLee/schema-driven-insight-agent/eino_agent"
	evalpkg "github.com/RuntianLee/schema-driven-insight-agent/eval_harness"
	"github.com/RuntianLee/schema-driven-insight-agent/eval_harness/evaluators"
	"github.com/RuntianLee/schema-driven-insight-agent/eval_harness/runners"
	"github.com/RuntianLee/schema-driven-insight-agent/eval_harness/tasks"
	"github.com/RuntianLee/schema-driven-insight-agent/llm"
	"github.com/RuntianLee/schema-driven-insight-agent/schema_protocol"
	"github.com/RuntianLee/schema-driven-insight-agent/tools"
	"github.com/RuntianLee/schema-driven-insight-agent/trajectory"
	"gopkg.in/yaml.v3"
)

// evalOrder 是报告列顺序，也是 gate 范围口径（仅 data_correctness 决定退出码）。
var evalOrder = []string{"data_correctness", "reasoning_quality", "insight_novelty"}

// FixtureFunc 为单个任务 seed 独立 fixture：dir 是本任务的临时目录，返回已就绪的
// Layer2 SQLite 连接（evalcli 负责 Close 与目录清理）。
type FixtureFunc func(dir string) (*sql.DB, error)

// Options 装配一次 eval 运行的全部输入。
type Options struct {
	Adapter      string                 // adapter 名（history meta + 临时目录前缀）
	SchemaPath   string                 // schema.yaml 路径
	TasksDir     string                 // 任务 YAML 目录
	Fixtures     map[string]FixtureFunc // 任务 ID → Go fixture seeder（逃生舱；YAML fixture 优先）
	SharedDBPath string                 // 共享 Layer2 db（YAML/Go fixture 都没有时的兜底；cmd/eval -db）
	OnlyTask     string                 // 只跑指定任务 ID；空跑全部
	UseRealLLM   bool                   // true → ResolveStrict 真道（agent+judge 共用）
	ConfigPath   string                 // 真道 LLM provider YAML
	OutDir       string                 // 报告落盘目录；空则不落盘
	TrajDBPath   string                 // trajectory 落库路径；空串不落库
	HistoryOut   string                 // PII-free verdict JSONL 追加路径；空则不写
	Commit       string                 // history 行的 commit SHA
}

// seedTaskDB 实现 fixture 三级解析（YAML > Go > -db 共享库），返回连接 + 清理函数。
func seedTaskDB(schema *schema_protocol.Schema, task tasks.Task, opts Options) (*sql.DB, func(), error) {
	goSeed, hasGo := opts.Fixtures[task.ID]
	switch {
	case !task.Fixture.IsZero():
		if hasGo {
			fmt.Fprintf(os.Stderr, "warn: 任务 %s 同时声明 YAML/Go fixture，YAML 优先\n", task.ID)
		}
		dir, err := os.MkdirTemp("", opts.Adapter+"eval-")
		if err != nil {
			return nil, nil, fmt.Errorf("mktemp: %w", err)
		}
		db, err := buildFixtureDB(schema, task.Fixture, dir)
		if err != nil {
			os.RemoveAll(dir)
			return nil, nil, fmt.Errorf("fixture %s: %w", task.ID, err)
		}
		return db, func() { db.Close(); os.RemoveAll(dir) }, nil
	case hasGo:
		dir, err := os.MkdirTemp("", opts.Adapter+"eval-")
		if err != nil {
			return nil, nil, fmt.Errorf("mktemp: %w", err)
		}
		db, err := goSeed(dir)
		if err != nil {
			os.RemoveAll(dir)
			return nil, nil, fmt.Errorf("seed %s: %w", task.ID, err)
		}
		return db, func() { db.Close(); os.RemoveAll(dir) }, nil
	case opts.SharedDBPath != "":
		// 预检：sqlite 的 Open 是惰性的，路径打错会被默默建成空库，把「路径错」
		// 变成下游「gate 失败」的迷雾。先 Stat 给出友好报错（恢复旧 cmd/eval 行为）。
		if _, statErr := os.Stat(opts.SharedDBPath); statErr != nil {
			return nil, nil, fmt.Errorf("共享 db %s 不可用（先跑 seed/ETL）: %w", opts.SharedDBPath, statErr)
		}
		db, err := sql.Open("sqlite", opts.SharedDBPath)
		if err != nil {
			return nil, nil, fmt.Errorf("open shared db %s: %w", opts.SharedDBPath, err)
		}
		return db, func() { db.Close() }, nil
	default:
		return nil, nil, fmt.Errorf("任务 %s 无 fixture（YAML fixture / Go fixture / -db 三选一）", task.ID)
	}
}

// judgeClientFor 按 opts.UseRealLLM 选取 judge client。
func judgeClientFor(opts Options, realJudge llm.Client) llm.Client {
	if opts.UseRealLLM {
		return realJudge
	}
	return evaluators.NewMockJudge()
}

// runPass 是可复用的单轮评测核心：对 taskList 逐任务 seed fixture → RunSuite → 合并报告。
// provider 非 nil 则开 reflection（config B）；nil 为 baseline（config A）。
// trajDB 非 nil 则落轨迹；nil 则纯内存。
func runPass(
	schema *schema_protocol.Schema, taskList []tasks.Task, opts Options,
	agentLLM, judge llm.Client, provider runners.ReflectionProvider, trajDB *sql.DB,
) (*evalpkg.Report, error) {
	merged := evalpkg.NewReport(evalOrder)
	for _, task := range taskList {
		if opts.OnlyTask != "" && task.ID != opts.OnlyTask {
			continue
		}
		db, cleanup, err := seedTaskDB(schema, task, opts)
		if err != nil {
			return nil, err
		}
		distTool := tools.NewDistributionTool(schema, db)
		reg := tools.NewRegistry()
		reg.Register("query_distribution", func(ctx context.Context, args map[string]any) (contract.Response, error) {
			return distTool.Run(ctx, tools.ArgsToQueryDistributionInput(args)), nil
		})
		analyzeTool := tools.NewAnalyzeTool(schema, db)
		reg.Register("analyze", func(ctx context.Context, args map[string]any) (contract.Response, error) {
			return analyzeTool.Run(ctx, tools.ArgsToAnalyzeInput(args)), nil
		})

		evalReg := evaluators.NewRegistry()
		evalReg.Register(evaluators.NewDataCorrectness())
		evalReg.Register(evaluators.NewReasoningQuality(judge))
		evalReg.Register(evaluators.NewInsightNovelty(judge))

		rep, err := runners.RunSuite(context.Background(), runners.Config{
			Dispatcher:         reg,
			SchemaCtx:          schema.Digest(),
			EvalReg:            evalReg,
			EvalOrder:          evalOrder,
			Tasks:              []runners.TaskInput{toTaskInput(task)},
			TrajDB:             trajDB,
			AgentLLM:           agentLLM,
			ReflectionProvider: provider,
		})
		cleanup()
		if err != nil {
			return nil, err
		}
		mergeInto(merged, rep)
	}
	return merged, nil
}

// Run 逐任务 seed fixture → RunSuite → 合并报告。
func Run(opts Options) (*evalpkg.Report, error) {
	schemaData, err := os.ReadFile(opts.SchemaPath)
	if err != nil {
		return nil, fmt.Errorf("read schema %s: %w", opts.SchemaPath, err)
	}
	schema, err := schema_protocol.Parse(schemaData)
	if err != nil {
		return nil, fmt.Errorf("parse schema: %w", err)
	}
	taskList, err := tasks.LoadDir(opts.TasksDir)
	if err != nil {
		return nil, fmt.Errorf("load tasks from %s: %w", opts.TasksDir, err)
	}

	var trajDB *sql.DB
	if opts.TrajDBPath != "" {
		trajDB, err = trajectory.Open(opts.TrajDBPath)
		if err != nil {
			return nil, fmt.Errorf("open trajectory db %s: %w", opts.TrajDBPath, err)
		}
		defer trajDB.Close()
		if err := trajectory.Migrate(trajDB); err != nil {
			return nil, fmt.Errorf("migrate trajectory db: %w", err)
		}
	}

	// 真道：解析一个真 client，同时喂 agent 与 judge（strict——无真 LLM 报错，不静默 mock）。
	var agentLLM llm.Client // nil → mock 道（RunSuite 退回 sequencedMock）
	var realJudge llm.Client
	if opts.UseRealLLM {
		real, err := llm.ResolveStrict(opts.ConfigPath)
		if err != nil {
			return nil, fmt.Errorf("真 LLM 评测道初始化失败: %w", err)
		}
		agentLLM, realJudge = real, real
	}

	merged, err := runPass(schema, taskList, opts, agentLLM, judgeClientFor(opts, realJudge), nil, trajDB)
	if err != nil {
		return nil, err
	}
	return merged, nil
}

// Finish 打印控制台表、落盘报告与 history（按 Options），返回进程退出码
// （0=gate 过，1=gate 失败）。落盘/写 history 失败仅 warn，不影响退出码口径。
func Finish(rep *evalpkg.Report, opts Options) int {
	fmt.Println(rep.ConsoleTable())
	if opts.OutDir != "" {
		if err := writeReports(rep, opts.OutDir); err != nil {
			fmt.Fprintln(os.Stderr, "warn: 写报告失败:", err)
		}
	}
	if opts.HistoryOut != "" {
		meta := evalpkg.HistoryMeta{
			Commit:       opts.Commit,
			Adapter:      opts.Adapter,
			AgentVersion: eino_agent.AgentVersion,
			RanAt:        time.Now().Unix(),
		}
		if err := evalpkg.AppendHistoryJSONL(opts.HistoryOut, rep, meta); err != nil {
			fmt.Fprintln(os.Stderr, "warn: 写 history 失败:", err)
		}
	}
	if rep.GateFailed() {
		return 1
	}
	return 0
}

func toTaskInput(t tasks.Task) runners.TaskInput {
	evals := make(map[string]yaml.Node, len(t.Evaluators))
	for k, v := range t.Evaluators {
		evals[k] = v
	}
	return runners.TaskInput{
		ID:         t.ID,
		Title:      t.Title,
		Question:   t.Question,
		LLMTurns:   t.LLMTurns,
		Evaluators: evals,
	}
}

func mergeInto(dst, src *evalpkg.Report) {
	for _, tid := range src.Tasks {
		for _, name := range evalOrder {
			if s, ok := src.Scores[tid][name]; ok {
				dst.Add(tid, s, name == "data_correctness")
			}
		}
	}
}

func writeReports(rep *evalpkg.Report, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	stamp := time.Now().Format("2006-01-02-150405") // 含时分秒：同日多次跑不互相覆盖
	js, err := rep.JSON()
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, stamp+"-report.json"), js, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, stamp+"-report.md"), []byte(rep.Markdown()), 0o644)
}
