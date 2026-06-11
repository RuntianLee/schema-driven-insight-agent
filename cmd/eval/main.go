// Command eval 跑任务集评测：确定性 mock 道（CI gate 默认）或真 LLM 道。
// 这是 README「eval harness gates data_correctness」的可运行入口。
//
// 用法（toygame 示例，仓库根目录，先 seed）：
//
//	go run ./examples/toygame/cmd/seed   # 在 examples/toygame 下执行
//	go run ./cmd/eval -schema examples/toygame/schema.yaml \
//	  -tasks examples/toygame/eval/tasks -db examples/toygame/data/toygame.db
//
// 退出码：0 = gate 通过；1 = gate 失败（data_correctness 任一任务不过）；2 = 运行错误。
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"

	"github.com/RuntianLee/schema-driven-insight-agent/contract"
	evalpkg "github.com/RuntianLee/schema-driven-insight-agent/eval_harness"
	"github.com/RuntianLee/schema-driven-insight-agent/eval_harness/evaluators"
	"github.com/RuntianLee/schema-driven-insight-agent/eval_harness/runners"
	"github.com/RuntianLee/schema-driven-insight-agent/eval_harness/tasks"
	"github.com/RuntianLee/schema-driven-insight-agent/llm"
	"github.com/RuntianLee/schema-driven-insight-agent/schema_protocol"
	"github.com/RuntianLee/schema-driven-insight-agent/tools"
	"github.com/RuntianLee/schema-driven-insight-agent/trajectory"
	"gopkg.in/yaml.v3"

	_ "modernc.org/sqlite"
)

type opts struct {
	schemaPath string
	tasksDir   string
	dbPath     string
	onlyTask   string
	trajDBPath string
	configPath string
	llmMode    string
}

func main() {
	var o opts
	flag.StringVar(&o.schemaPath, "schema", "schema.yaml", "schema.yaml 路径")
	flag.StringVar(&o.tasksDir, "tasks", "eval/tasks", "任务 YAML 目录")
	flag.StringVar(&o.dbPath, "db", "", "Layer2 SQLite 数据库路径（必填，先用 seed/ETL 生成）")
	flag.StringVar(&o.onlyTask, "task", "", "只跑指定任务 ID")
	flag.StringVar(&o.trajDBPath, "trajectory-db", "", "trajectory 落库路径（task_class=benchmark）；空串不落库")
	flag.StringVar(&o.configPath, "config", "config/llm.yaml", "LLM provider YAML（-llm minimax 时 agent+judge 共用）")
	flag.StringVar(&o.llmMode, "llm", "mock", "agent/judge LLM：mock（确定性，CI 默认）| minimax")
	flag.Parse()

	rep, err := run(o)
	if err != nil {
		fmt.Fprintln(os.Stderr, "eval 失败:", err)
		os.Exit(2)
	}
	fmt.Println(rep.ConsoleTable())
	if rep.GateFailed() {
		os.Exit(1)
	}
}

func run(o opts) (*evalpkg.Report, error) {
	if o.dbPath == "" {
		return nil, fmt.Errorf("-db 必填（Layer2 SQLite 路径，先用 seed/ETL 生成）")
	}
	if _, err := os.Stat(o.dbPath); err != nil {
		return nil, fmt.Errorf("数据库 %s 不可用（先跑 seed/ETL）: %w", o.dbPath, err)
	}
	schemaData, err := os.ReadFile(o.schemaPath)
	if err != nil {
		return nil, fmt.Errorf("read schema %s: %w", o.schemaPath, err)
	}
	schema, err := schema_protocol.Parse(schemaData)
	if err != nil {
		return nil, fmt.Errorf("parse schema: %w", err)
	}
	taskList, err := tasks.LoadDir(o.tasksDir)
	if err != nil {
		return nil, fmt.Errorf("load tasks from %s: %w", o.tasksDir, err)
	}
	inputs := make([]runners.TaskInput, 0, len(taskList))
	for _, t := range taskList {
		if o.onlyTask != "" && t.ID != o.onlyTask {
			continue
		}
		inputs = append(inputs, toTaskInput(t))
	}
	if len(inputs) == 0 {
		return nil, fmt.Errorf("无匹配任务（dir=%s, task=%q）", o.tasksDir, o.onlyTask)
	}

	db, err := sql.Open("sqlite", o.dbPath)
	if err != nil {
		return nil, fmt.Errorf("open db %s: %w", o.dbPath, err)
	}
	defer db.Close()

	var trajDB *sql.DB
	if o.trajDBPath != "" {
		trajDB, err = trajectory.Open(o.trajDBPath)
		if err != nil {
			return nil, fmt.Errorf("open trajectory db %s: %w", o.trajDBPath, err)
		}
		defer trajDB.Close()
		if err := trajectory.Migrate(trajDB); err != nil {
			return nil, fmt.Errorf("migrate trajectory db: %w", err)
		}
	}

	// 真道：解析一个真 client，同时喂 agent 与 judge（strict——无真 LLM 报错，不静默 mock）。
	var agentLLM llm.Client // nil → mock 道（RunSuite 退回 sequencedMock）
	var judge llm.Client = evaluators.NewMockJudge()
	if o.llmMode == "minimax" {
		real, err := llm.ResolveStrict(o.configPath)
		if err != nil {
			return nil, fmt.Errorf("真 LLM 评测道初始化失败: %w", err)
		}
		agentLLM, judge = real, real
	}

	distTool := tools.NewDistributionTool(schema, db)
	reg := tools.NewRegistry()
	reg.Register("query_distribution", func(ctx context.Context, args map[string]any) (contract.Response, error) {
		return distTool.Run(ctx, tools.ArgsToQueryDistributionInput(args)), nil
	})

	// 三个 evaluator 全部注册：任务选用但未注册会 loud-fail（RunSuite 配置校验）。
	evalReg := evaluators.NewRegistry()
	evalReg.Register(evaluators.NewDataCorrectness())
	evalReg.Register(evaluators.NewReasoningQuality(judge))
	evalReg.Register(evaluators.NewInsightNovelty(judge))

	evalOrder := []string{"data_correctness", "reasoning_quality", "insight_novelty"}
	return runners.RunSuite(context.Background(), runners.Config{
		Dispatcher: reg,
		SchemaCtx:  schema.Digest(),
		EvalReg:    evalReg,
		EvalOrder:  evalOrder,
		Tasks:      inputs,
		TrajDB:     trajDB,
		AgentLLM:   agentLLM,
	})
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
