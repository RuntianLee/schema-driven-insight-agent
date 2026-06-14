// Command eval 跑任务集评测：确定性 mock 道（CI gate 默认）或真 LLM 道。
// v0.2 起统一装配于 eval_harness/evalcli：任务可内联 fixture:（零代码 adapter），
// 或传 -db 共享单库（toygame quickstart 形态），或由 Go 调用方注入 FixtureFunc。
//
// 退出码：0 = gate 通过；1 = gate 失败（data_correctness）；2 = 运行错误。
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/RuntianLee/schema-driven-insight-agent/eval_harness/evalcli"

	_ "modernc.org/sqlite"
)

func main() {
	opts := evalcli.Options{}
	flag.StringVar(&opts.Adapter, "adapter", "adapter", "adapter 名（history meta + 临时目录前缀）")
	flag.StringVar(&opts.SchemaPath, "schema", "schema.yaml", "schema.yaml 路径")
	flag.StringVar(&opts.TasksDir, "tasks", "eval/tasks", "任务 YAML 目录")
	flag.StringVar(&opts.SharedDBPath, "db", "", "共享 Layer2 SQLite（任务无 fixture: 块时使用）")
	flag.StringVar(&opts.OnlyTask, "task", "", "只跑指定任务 ID")
	flag.StringVar(&opts.OutDir, "out", "", "报告落盘目录；空则不落盘")
	flag.StringVar(&opts.TrajDBPath, "trajectory-db", "", "trajectory 落库路径（task_class=benchmark）；空串不落库")
	flag.StringVar(&opts.HistoryOut, "history-out", "", "PII-free verdict 摘要 JSONL 追加路径；空则不写")
	flag.StringVar(&opts.Commit, "commit", "", "写入 history 行的 commit SHA（CI 传 $GITHUB_SHA）")
	flag.StringVar(&opts.ConfigPath, "config", "config/llm.yaml", "LLM provider YAML（-llm minimax 时 agent+judge 共用）")
	llmMode := flag.String("llm", "mock", "agent/judge LLM：mock（确定性，CI 默认）| minimax")
	mode := flag.String("mode", "suite", "suite（默认，确定性 gate）| ab（reflection 增益 A/B，需 -llm minimax，off-gate）")
	runs := flag.Int("runs", 5, "ab 模式每配置重复次数")
	flag.Parse()
	opts.UseRealLLM = (*llmMode == "minimax")

	if *mode == "ab" {
		ab, err := evalcli.RunAB(opts, nil, *runs) // nil provider：#5 baseline 对照；#4 wire 真 provider
		if err != nil {
			fmt.Fprintln(os.Stderr, "eval ab 失败:", err)
			os.Exit(2)
		}
		os.Exit(evalcli.FinishAB(ab, opts))
	}

	rep, err := evalcli.Run(opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "eval 失败:", err)
		os.Exit(2)
	}
	os.Exit(evalcli.Finish(rep, opts))
}
