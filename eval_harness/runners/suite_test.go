// framework/eval_harness/runners/suite_test.go
package runners

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/RuntianLee/schema-driven-insight-agent/contract"
	"github.com/RuntianLee/schema-driven-insight-agent/eval_harness/evaluators"
	"github.com/RuntianLee/schema-driven-insight-agent/schema_protocol"
	"github.com/RuntianLee/schema-driven-insight-agent/tools"
	"github.com/RuntianLee/schema-driven-insight-agent/trajectory"
	"gopkg.in/yaml.v3"

	_ "modernc.org/sqlite"
)

const testSchemaYAML = `
version: 1
domain: test_game
tuning:
  groups_top_n: 5
state_tables:
  player_basics:
    nature: snapshot
    primary_key: [player_id]
    fields:
      player_id:         {type: int64, role: actor_id, pk: true, pii: true}
      server_id:         {type: int32, role: dimension}
      level:             {type: int32, role: level}
      quest_level:   {type: int32, role: stage_progress}
      coins:             {type: int64, role: balance, currency_type: coins}
      last_online_time:  {type: unix_timestamp_seconds, role: last_seen}
derived_tables:
  player_currencies:
    derived_from: player_basics
    method: pivot_money_columns
    schema:
      player_id:     {type: int64,  role: actor_id}
      currency_type: {type: string, role: currency_kind, glossary_key: currency_types}
      balance:       {type: int64,  role: balance}
glossary:
  currency_types:
    coins: "coins (test)"
  buckets:
    coins_balance:
      - {min: 0,      max: 10000,  label: "0~1w"}
      - {min: 10001,  max: 100000, label: "1~10w"}
      - {min: 100001, max: 200000, label: "10~20w"}
      - {min: 200001, max: 500000, label: "20w~50w"}
      - {min: 500001, max: null,   label: "50w+"}
`

func loadTestSchema(t *testing.T) *schema_protocol.Schema {
	t.Helper()
	s, err := schema_protocol.Parse([]byte(testSchemaYAML))
	if err != nil {
		t.Fatalf("parse inline schema: %v", err)
	}
	return s
}

// newTestConfig 装配一个最小可跑的 RunSuite Config：真 query_distribution tool 连内存 fixture，
// data_correctness evaluator，一个驱动 mock 调用 query_distribution 并被 fixture 答中的任务。
// 复用本包既有 fixture（suiteFixtureDB / loadTestSchema / mustEvalNodes），与 TestRunSuiteEndToEnd 同形。
// 调用方按需设 cfg.TrajDB。
func newTestConfig(t *testing.T) Config {
	t.Helper()
	schema := loadTestSchema(t)
	db := suiteFixtureDB(t)

	distTool := tools.NewDistributionTool(schema, db)
	reg := tools.NewRegistry()
	reg.Register("query_distribution", func(ctx context.Context, args map[string]any) (contract.Response, error) {
		return distTool.Run(ctx, tools.ArgsToQueryDistributionInput(args)), nil
	})

	evalReg := evaluators.NewRegistry()
	evalReg.Register(evaluators.NewDataCorrectness())

	return Config{
		Dispatcher: reg,
		SchemaCtx:  schema.Digest(),
		EvalReg:    evalReg,
		EvalOrder:  []string{"data_correctness"},
		Tasks: []TaskInput{{
			ID:       "level_dist",
			Question: "等级分布？",
			LLMTurns: []string{
				`{"tool":"query_distribution","args":{"table":"player_basics","column":"level"}}`,
				"等级集中在 20（约 60%）。",
			},
			Evaluators: mustEvalNodes(t, map[string]string{
				"data_correctness": "tool: query_distribution\nexpect_status: OK\nrows:\n  - match: {bucket: \"20\"}\n    expect: {player_count: 150}\n",
			}),
		}},
	}
}

func TestRunSuite_PersistsToTrajDB(t *testing.T) {
	ctx := context.Background()
	db, err := trajectory.Open(filepath.Join(t.TempDir(), "eval.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := trajectory.Migrate(db); err != nil {
		t.Fatal(err)
	}

	cfg := newTestConfig(t) // 复用本包既有测试装配（见下）
	cfg.TrajDB = db

	if _, err := RunSuite(ctx, cfg); err != nil {
		t.Fatalf("RunSuite: %v", err)
	}

	var tc string
	if err := db.QueryRow(`SELECT task_class FROM trajectories LIMIT 1`).Scan(&tc); err != nil {
		t.Fatalf("no trajectory persisted: %v", err)
	}
	if tc != "benchmark" {
		t.Errorf("task_class = %q, want benchmark", tc)
	}
	var n int
	db.QueryRow(`SELECT count(*) FROM eval_results WHERE evaluator_name='data_correctness'`).Scan(&n)
	if n < 1 {
		t.Errorf("eval_results data_correctness rows = %d, want >=1", n)
	}
	// §9 跨版本 SQL 能跑出非空结果
	var ver string
	var pct float64
	if err := db.QueryRow(`
		SELECT t.agent_version, SUM(e.pass)*100.0/COUNT(*)
		FROM eval_results e JOIN trajectories t ON t.trajectory_id=e.trajectory_id
		WHERE e.evaluator_name='data_correctness' AND t.task_class='benchmark'
		GROUP BY t.agent_version`).Scan(&ver, &pct); err != nil {
		t.Fatalf("§9 cross-version query failed: %v", err)
	}
}

func TestRunSuite_NilTrajDB_BackwardCompat(t *testing.T) {
	ctx := context.Background()
	cfg := newTestConfig(t) // TrajDB 留 nil
	rep, err := RunSuite(ctx, cfg)
	if err != nil {
		t.Fatalf("RunSuite nil TrajDB: %v", err)
	}
	if rep == nil {
		t.Fatal("expected report even without TrajDB")
	}
}

// mustEvalNodes 把 map[string]string（YAML 片段）解析成 map[string]yaml.Node。
func mustEvalNodes(t *testing.T, m map[string]string) map[string]yaml.Node {
	t.Helper()
	out := make(map[string]yaml.Node, len(m))
	for k, v := range m {
		var n yaml.Node
		if err := yaml.Unmarshal([]byte(v), &n); err != nil {
			t.Fatalf("mustEvalNodes[%s]: yaml unmarshal: %v", k, err)
		}
		if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
			out[k] = *n.Content[0]
		} else {
			out[k] = n
		}
	}
	return out
}

// suiteFixtureDB 构建内存 SQLite player_basics：level 20×150, 50×90, 95×10（共 250）。
func suiteFixtureDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE player_basics (player_id TEXT, server_id INTEGER, level INTEGER, quest_level INTEGER, last_online_time INTEGER)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	stmt, err := tx.Prepare(`INSERT INTO player_basics VALUES ('p', 1, ?, 10, 1716000000)`)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	for _, lv := range []struct{ level, n int }{{20, 150}, {50, 90}, {95, 10}} {
		for i := 0; i < lv.n; i++ {
			if _, err := stmt.Exec(lv.level); err != nil {
				t.Fatalf("insert: %v", err)
			}
		}
	}
	stmt.Close()
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return db
}

// fakeAgentLLM 记录被调用次数，返回固定纯文本（被 runner 当作 final answer）。
type fakeAgentLLM struct {
	calls int
	resp  string
}

func (f *fakeAgentLLM) Call(_ context.Context, _ string) (string, int, int, float64, error) {
	f.calls++
	return f.resp, 1, 1, 0, nil
}
func (f *fakeAgentLLM) Model() string { return "fake-agent" }

func TestRunSuiteAgentLLMInjection(t *testing.T) {
	fake := &fakeAgentLLM{resp: "（fake）最终回答"}
	cfg := Config{
		Dispatcher: tools.NewRegistry(), // 空 registry：fake 返回纯文本，不触发 tool 调用
		SchemaCtx:  "schema-x",
		EvalReg:    evaluators.NewRegistry(),
		EvalOrder:  nil,
		AgentLLM:   fake, // 非 nil → 用注入 client 而非 sequencedMock
		Tasks: []TaskInput{{
			ID:       "t1",
			Question: "随便问",
			LLMTurns: nil, // 故意为空：若仍走 sequencedMock 会 exhausted，证明被忽略
		}},
	}
	if _, err := RunSuite(context.Background(), cfg); err != nil {
		t.Fatalf("RunSuite error: %v", err)
	}
	if fake.calls == 0 {
		t.Fatal("AgentLLM 被注入却从未被调用——注入口未生效")
	}
}

func TestRunSuiteEndToEnd(t *testing.T) {
	schema := loadTestSchema(t)
	db := suiteFixtureDB(t)

	distTool := tools.NewDistributionTool(schema, db)
	reg := tools.NewRegistry()
	reg.Register("query_distribution", func(ctx context.Context, args map[string]any) (contract.Response, error) {
		return distTool.Run(ctx, tools.ArgsToQueryDistributionInput(args)), nil
	})

	evalReg := evaluators.NewRegistry()
	evalReg.Register(evaluators.NewDataCorrectness())
	evalReg.Register(evaluators.NewReasoningQuality(evaluators.NewMockJudge()))

	cfg := Config{
		Dispatcher: reg,
		SchemaCtx:  schema.Digest(),
		EvalReg:    evalReg,
		EvalOrder:  []string{"data_correctness", "reasoning_quality"},
		Tasks: []TaskInput{{
			ID:       "level_dist",
			Question: "等级分布？",
			LLMTurns: []string{
				`{"tool":"query_distribution","args":{"table":"player_basics","column":"level"}}`,
				"等级集中在 20（约 60%）。",
			},
			Evaluators: mustEvalNodes(t, map[string]string{
				"data_correctness":  "tool: query_distribution\nexpect_status: OK\nrows:\n  - match: {bucket: \"20\"}\n    expect: {player_count: 150}\n",
				"reasoning_quality": "rubric: 量化集中度？",
			}),
		}},
	}

	rep, err := RunSuite(context.Background(), cfg)
	if err != nil {
		t.Fatalf("run suite: %v", err)
	}
	if rep.GateFailed() {
		t.Fatalf("gate should pass: %s", rep.ConsoleTable())
	}
	if rep.Scores["level_dist"]["data_correctness"].Value != 1.0 {
		t.Fatalf("data_correctness should be 1.0:\n%s", rep.ConsoleTable())
	}
}
