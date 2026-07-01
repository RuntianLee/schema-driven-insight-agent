// framework/eval_harness/evalcli/ab.go
package evalcli

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"

	"github.com/RuntianLee/schema-driven-insight-agent/eino_agent"
	evalpkg "github.com/RuntianLee/schema-driven-insight-agent/eval_harness"
	"github.com/RuntianLee/schema-driven-insight-agent/eval_harness/reflexion"
	"github.com/RuntianLee/schema-driven-insight-agent/eval_harness/runners"
	"github.com/RuntianLee/schema-driven-insight-agent/eval_harness/tasks"
	"github.com/RuntianLee/schema-driven-insight-agent/llm"
	"github.com/RuntianLee/schema-driven-insight-agent/memory"
	"github.com/RuntianLee/schema-driven-insight-agent/schema_protocol"
	"github.com/RuntianLee/schema-driven-insight-agent/trajectory"
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
	snapshotBefore := memorySnapshotID(opts.MemoryDBPath)
	// 真道：agentLLM=nil, agentModel=nil → runPass 走 opts.UseRealLLM 分支建 ChatModel；judge 仍用 real。
	ab, err := runABWithClients(opts, nil, real, nil, provider, runs, attempts, labelB)
	if err != nil {
		return nil, err
	}
	snapshotAfter := memorySnapshotID(opts.MemoryDBPath)
	var hits reflexion.HitStats
	if pp, ok := provider.(*reflexion.PersistentProvider); ok {
		hits = pp.HitStats()
	}
	annotateMemoryABReport(ab, opts, labelB, snapshotBefore, snapshotAfter, hits)
	return ab, nil
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
	allowedFields, err := allowedFieldsForMemory(opts.SchemaPath)
	if err != nil {
		db.Close()
		return nil, nil, "", err
	}
	store := memory.NewSQLiteStore(db)
	provider := reflexion.NewPersistent(reflectLLM, store, reflexion.PersistentOptions{
		Adapter:             opts.Adapter,
		TaskClass:           "benchmark",
		ContextOptions:      memory.ContextOptions{MaxItems: opts.MemoryLimit, MaxChars: 1600},
		Limit:               opts.MemoryLimit,
		MinScore:            opts.MemoryMinScore,
		PersistObservations: opts.MemoryWrite,
		AllowedFields:       allowedFields,
	})
	cleanup := func() { _ = store.Close() }
	return provider, cleanup, "reflection+memory", nil
}

func allowedFieldsForMemory(schemaPath string) ([]string, error) {
	if schemaPath == "" {
		return nil, nil
	}
	schemaData, err := os.ReadFile(schemaPath)
	if err != nil {
		return nil, fmt.Errorf("read schema %s for memory fields: %w", schemaPath, err)
	}
	schema, err := schema_protocol.Parse(schemaData)
	if err != nil {
		return nil, fmt.Errorf("parse schema for memory fields: %w", err)
	}
	seen := map[string]bool{}
	for _, tbl := range schema.StateTables {
		for field := range tbl.Fields {
			seen[field] = true
		}
	}
	for _, tbl := range schema.DerivedTables {
		for field := range tbl.Fields {
			seen[field] = true
		}
	}
	fields := make([]string, 0, len(seen))
	for field := range seen {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	return fields, nil
}

// runABWithClients 是 RunAB 的可测内核：client/provider/agentModel 由调用方注入（测试传 fake）。
// agentModel 非 nil 则 agent 腿用注入模型（测试 fake eino model）；nil 则由 runPass 按 opts.UseRealLLM 决定。
// config A（baseline）：provider=nil 冷跑 1 次；config B：每样本 Reset 后跑 attempts 次 reflexion，取末次。
func runABWithClients(opts Options, agentLLM, judge llm.Client, agentModel model.ToolCallingChatModel, provider runners.ReflectionProvider, runs, attempts int, labelB string) (*evalpkg.ABReport, error) {
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

	trajDB, cleanup, err := trajectoryDBForAB(opts)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	var aReports, bReports []*evalpkg.Report
	for i := 0; i < runs; i++ {
		ra, err := runPass(schema, taskList, withTaskClass(opts, "benchmark:ab:baseline"), agentLLM, judge, agentModel, nil, trajDB) // A: baseline 冷跑 1 次
		if err != nil {
			return nil, fmt.Errorf("A run %d: %w", i, err)
		}
		// 新 reflexion 序列冷起（临时内存清空）。
		if r, ok := provider.(interface{ Reset() }); ok {
			r.Reset()
		}
		var rb *evalpkg.Report
		for k := 0; k < attempts; k++ {
			taskClass := fmt.Sprintf("benchmark:ab:%s:attempt%d", labelB, k+1)
			rb, err = runPass(schema, taskList, withTaskClass(opts, taskClass), agentLLM, judge, agentModel, provider, trajDB) // B: 第 k 次 reflexion 尝试
			if err != nil {
				return nil, fmt.Errorf("B run %d attempt %d: %w", i, k, err)
			}
		}
		aReports = append(aReports, ra)
		bReports = append(bReports, rb) // 只把第 attempts 次（收敛后）计入聚合
	}
	return evalpkg.BuildABReport("baseline", labelB, runs, aReports, bReports)
}

func trajectoryDBForAB(opts Options) (*sql.DB, func(), error) {
	if opts.TrajDBPath == "" {
		return nil, func() {}, nil
	}
	db, err := trajectory.Open(opts.TrajDBPath)
	if err != nil {
		return nil, nil, fmt.Errorf("open trajectory db %s: %w", opts.TrajDBPath, err)
	}
	if err := trajectory.Migrate(db); err != nil {
		db.Close()
		return nil, nil, fmt.Errorf("migrate trajectory db: %w", err)
	}
	return db, func() { _ = db.Close() }, nil
}

func withTaskClass(opts Options, taskClass string) Options {
	opts.TaskClass = taskClass
	return opts
}

func memorySnapshotID(path string) string {
	if path == "" {
		return ""
	}
	parts := []string{path, path + "-wal"}
	h := sha256.New()
	wrote := false
	for _, p := range parts {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		h.Write([]byte(p))
		h.Write([]byte{0})
		h.Write(data)
		wrote = true
	}
	if !wrote {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

func annotateMemoryABReport(ab *evalpkg.ABReport, opts Options, labelB, snapshotBefore, snapshotAfter string, hits reflexion.HitStats) {
	if ab == nil {
		return
	}
	ab.ReflectionMode = labelB
	ab.MemoryEnabled = opts.MemoryDBPath != ""
	ab.MemoryDBPath = opts.MemoryDBPath
	ab.MemorySnapshot = snapshotBefore
	ab.MemorySnapshotBefore = snapshotBefore
	ab.MemorySnapshotAfter = snapshotAfter
	ab.MemorySnapshotStable = snapshotBefore != "" && snapshotBefore == snapshotAfter
	ab.MemoryWrite = opts.MemoryWrite
	ab.MemoryRetrievalPolicy = memoryRetrievalPolicy(opts)
	ab.MemoryHitsExactTask = hits.ExactTask
	ab.MemoryHitsSameClass = hits.SameClass
	ab.MemoryHitsSimilarQuestion = hits.SimilarQuestion
	ab.MemoryHitsOnFacet = hits.OnFacet
	ab.MemoryHitsOffFacet = hits.OffFacet
	if ab.MemoryEnabled && !ab.MemoryWrite && !ab.MemorySnapshotStable {
		appendABCaveat(ab, "Memory snapshot changed during read-only A/B run; do not treat this report as a fixed-snapshot measurement.")
	}
}

func memoryRetrievalPolicy(opts Options) string {
	if opts.MemoryDBPath == "" {
		return ""
	}
	return "same_task_then_similar_question"
}

func appendABCaveat(ab *evalpkg.ABReport, msg string) {
	msg = strings.TrimSpace(msg)
	if ab == nil || msg == "" {
		return
	}
	if strings.TrimSpace(ab.Caveat) == "" {
		ab.Caveat = msg
		return
	}
	ab.Caveat = strings.TrimSpace(ab.Caveat) + "\n" + msg
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
