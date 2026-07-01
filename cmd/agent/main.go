// Package main 是 V0 唯一对外入口（CLI）。无 HTTP/前端。
//
// 两种输入模式：
//
//	-q "问题"   单发模式：跑一次即退出（T10 演示彩排 / X1 自动化）
//	(无 flag)   REPL 模式：循环读 stdin → Run → 打印表+洞察（现场演示，运营可追问）
//
// 启动顺序（design-v3 §13）：etl_health.Ready() 校验 → 未就绪 exit 1。
package main

import (
	"bufio"
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/RuntianLee/schema-driven-insight-agent/advisor"
	"github.com/RuntianLee/schema-driven-insight-agent/agent"
	"github.com/RuntianLee/schema-driven-insight-agent/contract"
	"github.com/RuntianLee/schema-driven-insight-agent/eino_agent"
	"github.com/cloudwego/eino/components/model"
	"github.com/RuntianLee/schema-driven-insight-agent/etl_health"
	"github.com/RuntianLee/schema-driven-insight-agent/llm"
	"github.com/RuntianLee/schema-driven-insight-agent/prompts"
	"github.com/RuntianLee/schema-driven-insight-agent/schema_protocol"
	"github.com/RuntianLee/schema-driven-insight-agent/tools"
	"github.com/RuntianLee/schema-driven-insight-agent/trajcapture"
	"github.com/RuntianLee/schema-driven-insight-agent/trajectory"

	_ "modernc.org/sqlite"
)

// ── main ───────────────────────────────────────────────────────────────────────

func main() {
	q := flag.String("q", "", "单发模式：提问后退出（留空则进入 REPL）")
	advise := flag.Bool("advise", false, "同跑自动接力：Analyst 跑完自动调 Advisor 产出建议草案")
	configPath := flag.String("config", "./config/llm.yaml", "LLM provider YAML config 文件路径")
	flag.Parse()

	ctx := context.Background()

	// ── 1. ETL 健康检查（design-v3 §13）────────────────────────────────
	healthPath := envOrDefault("ETL_HEALTH_PATH", "./examples/toygame/data/etl_health.json")
	h, err := etl_health.Read(healthPath)
	if err != nil {
		log.Fatalf("ETL health read failed: %v", err)
	}
	if ok, reason := h.Ready(); !ok {
		log.Fatalf("ETL not ready: %s", reason)
	}

	// ── 2. 加载 schema ───────────────────────────────────────────────────
	schemaPath := envOrDefault("SCHEMA_PATH", "./examples/toygame/schema.yaml")
	schemaBytes, err := os.ReadFile(schemaPath)
	if err != nil {
		log.Fatalf("schema read %s: %v", schemaPath, err)
	}
	schema, err := schema_protocol.Parse(schemaBytes)
	if err != nil {
		log.Fatalf("schema parse: %v", err)
	}

	// ── 3. 打开业务 SQLite ───────────────────────────────────────────────
	sqlitePath := envOrDefault("SQLITE_PATH", "./examples/toygame/data/toygame.db")
	bizDB, err := sql.Open("sqlite", sqlitePath)
	if err != nil {
		log.Fatalf("open layer2 db %s: %v", sqlitePath, err)
	}
	defer bizDB.Close()
	if err := bizDB.PingContext(ctx); err != nil {
		log.Fatalf("ping layer2 db: %v", err)
	}
	if err := checkBizSchemaVersion(bizDB, schema.Version); err != nil {
		log.Fatalf("ETL not ready: %v", err)
	}

	// ── 4. 打开 + 迁移 trajectory DB ────────────────────────────────────
	trajPath := envOrDefault("TRAJECTORY_DB_PATH", "./trajectory.db")
	trajDB, err := trajectory.Open(trajPath)
	if err != nil {
		log.Fatalf("open trajectory.db %s: %v", trajPath, err)
	}
	defer trajDB.Close()
	if err := trajectory.Migrate(trajDB); err != nil {
		log.Fatalf("migrate trajectory.db: %v", err)
	}

	// ── 5. 构建 LLM client（config > env > mock）───────────────────────────
	client, err := llm.Resolve(*configPath)
	if err != nil {
		log.Fatalf("LLM 客户端初始化失败: %v", err)
	}

	// Agent 腿：从同一 config 建 anthropic-wire ChatModel（judge/advisor 仍用上面的 client）。
	cfgData, err := os.ReadFile(*configPath)
	if err != nil {
		log.Fatalf("读取 LLM config 失败（Agent 腿需 anthropic 配置，不再退回 mock）: %v", err)
	}
	agentCfg, err := llm.ParseMiniMaxConfig(cfgData)
	if err != nil {
		log.Fatalf("解析 LLM config 失败: %v", err)
	}
	chatModel, err := eino_agent.NewChatModel(ctx, agentCfg)
	if err != nil {
		log.Fatalf("构建 Agent ChatModel 失败: %v", err)
	}

	// ── 6. 构建 tools.Registry ────────────────────────────────────────────
	distTool := tools.NewDistributionTool(schema, bizDB)
	registry := tools.NewRegistry()
	registry.Register("query_distribution", func(ctx context.Context, args map[string]any) (contract.Response, error) {
		in, errResp := tools.ParseQueryDistributionArgs(args)
		if errResp != nil {
			return *errResp, nil
		}
		return distTool.Run(ctx, in), nil
	})
	analyzeTool := tools.NewAnalyzeTool(schema, bizDB)
	registry.Register("analyze", func(ctx context.Context, args map[string]any) (contract.Response, error) {
		in, errResp := tools.ParseAnalyzeArgs(args)
		if errResp != nil {
			return *errResp, nil
		}
		return analyzeTool.Run(ctx, in), nil
	})

	// ── 7. 构建 Runner ────────────────────────────────────────────────────
	opener := func(ctx context.Context, agentVersion, question string) (agent.TrajectoryStore, error) {
		return trajectory.New(ctx, trajDB, agentVersion, question, "production")
	}
	digest := schema.Digest()
	runner := eino_agent.New(chatModel, agentCfg.Model, registry, opener, digest)

	// ── 选择模式 ──────────────────────────────────────────────────────────
	if *advise && *q == "" {
		log.Fatal("-advise 必须配合 -q 使用（REPL 暂不支持）")
	}
	if *q != "" {
		if *advise {
			playbook := ""
			if schema.Advisor != nil {
				playbook = schema.Advisor.Playbook
			}
			answer, draft, err := runAdvisePipeline(ctx, client, chatModel, agentCfg.Model, registry, digest, playbook, *q)
			if err != nil {
				log.Fatalf("advise pipeline error: %v", err)
			}
			fmt.Println(answer)
			fmt.Print(renderAdvisory(draft))
		} else {
			runOnce(ctx, runner, *q)
		}
	} else {
		runREPL(ctx, runner)
	}
}

// runAdvisePipeline 同跑自动接力：Analyst 跑一遍（经 Capture 收结构化结果）→ 构 AnalystOutput → Advisor 出草案。
func runAdvisePipeline(ctx context.Context, client llm.Client, chatModel model.ToolCallingChatModel, modelName string,
	registry agent.ToolDispatcher, schemaDigest, playbook, question string) (string, contract.AdvisoryDraft, error) {
	cap := trajcapture.New()
	opener := func(_ context.Context, _, _ string) (agent.TrajectoryStore, error) { return cap, nil }
	runner := eino_agent.New(chatModel, modelName, registry, opener, schemaDigest)
	answer, err := runner.Run(ctx, question)
	if err != nil {
		return "", contract.AdvisoryDraft{}, err
	}
	out := contract.AnalystOutput{Question: question, Results: cap.AnalystResults(), Narrative: answer}
	draft, err := advisor.New(client, prompts.AdvisorV0).Advise(ctx, out, playbook)
	if err != nil {
		return answer, contract.AdvisoryDraft{}, err
	}
	return answer, draft, nil
}

// renderAdvisory 把草案渲染成人读文本（明确标注推测性）。
func renderAdvisory(d contract.AdvisoryDraft) string {
	var b strings.Builder
	b.WriteString("\n── 建议草案（推测性，需业务方验证）──\n")
	if d.Summary != "" {
		b.WriteString(d.Summary + "\n")
	}
	for i, it := range d.Items {
		fmt.Fprintf(&b, "%d. [%s] %s\n   依据(%s): %s\n   注: %s\n", i+1, it.Priority, it.Action, it.SourceRef, it.Observation, it.Caveat)
	}
	if len(d.Items) == 0 {
		b.WriteString("（无可靠依据，未产出建议）\n")
	}
	return b.String()
}

// runOnce 单发模式：Run 一次后退出。
func runOnce(ctx context.Context, runner *eino_agent.Runner, question string) {
	answer, err := runner.Run(ctx, question)
	if err != nil {
		log.Fatalf("run error: %v", err)
	}
	fmt.Println(answer)
}

// runREPL REPL 模式：循环读 stdin，空行/exit/EOF 退出。
func runREPL(ctx context.Context, runner *eino_agent.Runner) {
	sc := bufio.NewScanner(os.Stdin)
	fmt.Println("数据分析 Agent V0（输入问题回车，空行/exit/EOF 退出）")
	for {
		fmt.Print("> ")
		if !sc.Scan() {
			break // EOF
		}
		line := strings.TrimSpace(sc.Text())
		if line == "" || line == "exit" {
			break
		}
		answer, err := runner.Run(ctx, line)
		if err != nil {
			fmt.Fprintf(os.Stderr, "错误：%v\n", err)
			continue
		}
		fmt.Println(answer)
		fmt.Println()
	}
	fmt.Println("再见。")
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// checkBizSchemaVersion reads layer2 db's _meta schema_version and compares to want.
// Missing table/row or mismatch returns an error with a remediation hint.
func checkBizSchemaVersion(db *sql.DB, want int) error {
	var val string
	err := db.QueryRow(`SELECT value FROM _meta WHERE key='schema_version'`).Scan(&val)
	if err != nil {
		// sql.ErrNoRows or "no such table" both land here.
		return fmt.Errorf("layer2 db schema version unknown (run: seed/ETL first)")
	}
	got, err := strconv.Atoi(val)
	if err != nil {
		return fmt.Errorf("layer2 db schema version unparseable %q (run: seed/ETL first)", val)
	}
	if got != want {
		return fmt.Errorf("layer2 db schema version %d != schema.yaml %d (re-run ETL / migrate)", got, want)
	}
	return nil
}
