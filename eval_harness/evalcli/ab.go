// framework/eval_harness/evalcli/ab.go
package evalcli

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/RuntianLee/schema-driven-insight-agent/eino_agent"
	evalpkg "github.com/RuntianLee/schema-driven-insight-agent/eval_harness"
	"github.com/RuntianLee/schema-driven-insight-agent/eval_harness/reflexion"
	"github.com/RuntianLee/schema-driven-insight-agent/eval_harness/runners"
	"github.com/RuntianLee/schema-driven-insight-agent/eval_harness/tasks"
	"github.com/RuntianLee/schema-driven-insight-agent/llm"
	"github.com/RuntianLee/schema-driven-insight-agent/memory"
	"github.com/RuntianLee/schema-driven-insight-agent/schema_protocol"
)

// RunAB 解析真 LLM client（agent+judge+reflect 共用），跑 Design β A/B。
// attempts = 每个独立样本内 reflexion 尝试次数 K（取第 K 次计入）；attempts<=1 等价无累积。
func RunAB(opts Options, runs, attempts int) (*evalpkg.ABReport, error) {
	if !opts.UseRealLLM {
		return nil, fmt.Errorf("A/B 度量需真 LLM 道（加 -llm minimax）")
	}
	real, err := llm.ResolveStrict(opts.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("真 LLM 初始化失败: %w", err)
	}
	provider, cleanup, labelB, err := reflectionProviderForAB(opts, real)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	return runABWithClients(opts, real, real, provider, runs, attempts, labelB)
}

func reflectionProviderForAB(opts Options, reflectLLM llm.Client) (runners.ReflectionProvider, func(), string, error) {
	if opts.MemoryDBPath == "" {
		return reflexion.New(reflectLLM), func() {}, "reflection", nil
	}

	db, err := memory.Open(opts.MemoryDBPath)
	if err != nil {
		return nil, nil, "", fmt.Errorf("open memory db %s: %w", opts.MemoryDBPath, err)
	}
	if err := memory.Migrate(db); err != nil {
		db.Close()
		return nil, nil, "", fmt.Errorf("migrate memory db: %w", err)
	}
	store := memory.NewSQLiteStore(db)
	provider := reflexion.NewPersistent(reflectLLM, store, reflexion.PersistentOptions{
		Adapter:             opts.Adapter,
		TaskClass:           "benchmark",
		ContextOptions:      memory.ContextOptions{MaxItems: opts.MemoryLimit, MaxChars: 1600},
		Limit:               opts.MemoryLimit,
		MinScore:            opts.MemoryMinScore,
		PersistObservations: opts.MemoryWrite,
	})
	cleanup := func() { _ = store.Close() }
	return provider, cleanup, "reflection+memory", nil
}

// runABWithClients 是 RunAB 的可测内核：client/provider 由调用方注入（测试传 fake）。
// config A（baseline）：provider=nil 冷跑 1 次；config B：每样本 Reset 后跑 attempts 次 reflexion，取末次。
func runABWithClients(opts Options, agentLLM, judge llm.Client, provider runners.ReflectionProvider, runs, attempts int, labelB string) (*evalpkg.ABReport, error) {
	if runs <= 0 {
		return nil, fmt.Errorf("runs 必须 > 0")
	}
	if attempts < 1 {
		attempts = 1
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

	// trajDB 恒传 nil：A/B 只产出 ABReport 聚合。
	var aReports, bReports []*evalpkg.Report
	for i := 0; i < runs; i++ {
		ra, err := runPass(schema, taskList, opts, agentLLM, judge, nil, nil) // A: baseline 冷跑 1 次
		if err != nil {
			return nil, fmt.Errorf("A run %d: %w", i, err)
		}
		// 新 reflexion 序列冷起（临时内存清空）。
		if r, ok := provider.(interface{ Reset() }); ok {
			r.Reset()
		}
		var rb *evalpkg.Report
		for k := 0; k < attempts; k++ {
			rb, err = runPass(schema, taskList, opts, agentLLM, judge, provider, nil) // B: 第 k 次 reflexion 尝试
			if err != nil {
				return nil, fmt.Errorf("B run %d attempt %d: %w", i, k, err)
			}
		}
		aReports = append(aReports, ra)
		bReports = append(bReports, rb) // 只把第 attempts 次（收敛后）计入聚合
	}
	return evalpkg.BuildABReport("baseline", labelB, runs, aReports, bReports)
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
	if opts.HistoryOut != "" {
		meta := evalpkg.ABHistoryMeta{
			Commit:       opts.Commit,
			Adapter:      opts.Adapter,
			AgentVersion: eino_agent.AgentVersion,
			RanAt:        time.Now().Unix(),
		}
		if err := evalpkg.AppendABHistoryJSONL(opts.HistoryOut, ab, meta); err != nil {
			fmt.Fprintln(os.Stderr, "warn: 写 ab-history 失败:", err)
		}
	}
	return 0
}
