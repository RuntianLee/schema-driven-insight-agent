// framework/eval_harness/evalcli/ab.go
package evalcli

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	evalpkg "github.com/RuntianLee/schema-driven-insight-agent/eval_harness"
	"github.com/RuntianLee/schema-driven-insight-agent/eval_harness/runners"
	"github.com/RuntianLee/schema-driven-insight-agent/eval_harness/tasks"
	"github.com/RuntianLee/schema-driven-insight-agent/llm"
	"github.com/RuntianLee/schema-driven-insight-agent/schema_protocol"
)

// RunAB 解析真 LLM client（agent+judge 共用），跑 A/B。
// 需要 opts.UseRealLLM=true，否则报错（A/B 度量需真 LLM 道）。
func RunAB(opts Options, provider runners.ReflectionProvider, runs int) (*evalpkg.ABReport, error) {
	if !opts.UseRealLLM {
		return nil, fmt.Errorf("A/B 度量需真 LLM 道（加 -llm minimax）")
	}
	real, err := llm.ResolveStrict(opts.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("真 LLM 初始化失败: %w", err)
	}
	return runABWithClients(opts, real, real, provider, runs)
}

// runABWithClients 是 RunAB 的可测内核：client 由调用方注入（测试传 fake）。
// config A（baseline）：provider=nil，reflection 关；config B：provider 注入上下文。
func runABWithClients(opts Options, agentLLM, judge llm.Client, provider runners.ReflectionProvider, runs int) (*evalpkg.ABReport, error) {
	if runs <= 0 {
		return nil, fmt.Errorf("runs 必须 > 0")
	}
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
		return nil, fmt.Errorf("load tasks %s: %w", opts.TasksDir, err)
	}

	// trajDB 恒传 nil：A/B 只产出 ABReport 聚合，不落轨迹库（variant tag 落库推到 #4，
	// 见 spec WS-3 §B3.6）。两遍仅靠 provider 区分（A=nil 关、B=provider 开）。
	var aReports, bReports []*evalpkg.Report
	for i := 0; i < runs; i++ {
		ra, err := runPass(schema, taskList, opts, agentLLM, judge, nil, nil)
		if err != nil {
			return nil, fmt.Errorf("A run %d: %w", i, err)
		}
		rb, err := runPass(schema, taskList, opts, agentLLM, judge, provider, nil)
		if err != nil {
			return nil, fmt.Errorf("B run %d: %w", i, err)
		}
		aReports = append(aReports, ra)
		bReports = append(bReports, rb)
	}
	return evalpkg.BuildABReport("baseline", "reflection", runs, aReports, bReports)
}

// FinishAB 打印 A/B 摘要、落盘 JSON（off-gate：恒返回 0）。
func FinishAB(ab *evalpkg.ABReport, opts Options) int {
	fmt.Println(ab.ConsoleTable())
	if opts.OutDir != "" {
		if err := os.MkdirAll(opts.OutDir, 0o755); err != nil {
			fmt.Fprintln(os.Stderr, "warn: 建报告目录失败:", err)
		} else {
			stamp := time.Now().Format("2006-01-02-150405")
			if js, err := ab.JSON(); err == nil {
				_ = os.WriteFile(filepath.Join(opts.OutDir, stamp+"-ab-report.json"), js, 0o644)
			}
		}
	}
	return 0
}
