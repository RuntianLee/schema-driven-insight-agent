package eino_agent

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RuntianLee/schema-driven-insight-agent/agent"
	"github.com/RuntianLee/schema-driven-insight-agent/contract"
	"github.com/RuntianLee/schema-driven-insight-agent/llm"
	"github.com/RuntianLee/schema-driven-insight-agent/schema_protocol"
	"github.com/RuntianLee/schema-driven-insight-agent/tools"
	"github.com/RuntianLee/schema-driven-insight-agent/trajectory"

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

func loadSchema(t *testing.T) *schema_protocol.Schema {
	t.Helper()
	s, err := schema_protocol.Parse([]byte(testSchemaYAML))
	if err != nil {
		t.Fatalf("parse inline schema: %v", err)
	}
	return s
}

// seqMock is a sequential LLM mock that returns scripted responses in order.
// This avoids the substring-ambiguity problem that arises when a cumulative
// conversation prompt contains keys from multiple turns.
type seqMock struct {
	responses []string
	idx       int
}

func (s *seqMock) Call(_ context.Context, _ string) (string, int, int, float64, error) {
	if s.idx >= len(s.responses) {
		return "（seqMock exhausted）", 1, 1, 0, nil
	}
	r := s.responses[s.idx]
	s.idx++
	return r, len(r) / 4, len(r) / 4, 0, nil
}
func (s *seqMock) Model() string { return "mock-seq" }

// Verify seqMock satisfies llm.Client at compile time.
var _ llm.Client = (*seqMock)(nil)

func fixtureDB(t *testing.T) *sql.DB {
	t.Helper()
	db, _ := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	t.Cleanup(func() { db.Close() })
	db.Exec(`CREATE TABLE player_currencies (player_id TEXT, currency_type TEXT, balance INTEGER)`)
	tx, _ := db.Begin()
	stmt, _ := tx.Prepare(`INSERT INTO player_currencies VALUES ('p', 'coins', ?)`)
	insert := func(balance int64, n int) {
		for i := 0; i < n; i++ {
			stmt.Exec(balance)
		}
	}
	insert(5000, 200)
	insert(50000, 150)
	insert(600000, 50)
	stmt.Close()
	tx.Commit()
	return db
}

func TestRunner_MockLLM_ToolRouteAndTrajectory(t *testing.T) {
	ctx := context.Background()
	schema := loadSchema(t)
	bizDB := fixtureDB(t)

	// trajectory DB
	trajDB, err := trajectory.Open(filepath.Join(t.TempDir(), "traj.db"))
	if err != nil {
		t.Fatalf("traj open: %v", err)
	}
	t.Cleanup(func() { trajDB.Close() })
	if err := trajectory.Migrate(trajDB); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// tools
	reg := tools.NewRegistry()
	distTool := tools.NewDistributionTool(schema, bizDB)
	reg.Register("query_distribution", func(c context.Context, args map[string]any) (contract.Response, error) {
		in := tools.QueryDistributionInput{
			Table:     str(args["table"]),
			Column:    str(args["column"]),
			BucketKey: str(args["bucket_key"]),
			Filter:    map[string]any{"currency_type": "coins"},
		}
		return distTool.Run(c, in), nil
	})

	// Sequential mock LLM: turn1 → tool call JSON; turn2 → final natural-language report.
	// Uses call-order (not substring matching) to avoid ambiguity across cumulative prompts.
	mock := &seqMock{
		responses: []string{
			// turn 1: instruct the runner to call query_distribution
			`{"tool":"query_distribution","args":{"table":"player_currencies","column":"balance","bucket_key":"coins_balance"}}`,
			// turn 2: final answer referencing ROI — satisfies the test assertion
			"报告：头部玩家集中度显著，1~10w 段是 ROI 最高的运营目标群。",
		},
	}

	opener := func(c context.Context, ver, q string) (agent.TrajectoryStore, error) {
		return trajectory.New(c, trajDB, ver, q, "benchmark")
	}
	runner := New(mock, reg, opener, schema.Digest())

	answer, err := runner.Run(ctx, "当前货币分布是怎样的？")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if answer == "" || !strings.Contains(answer, "ROI") {
		t.Fatalf("unexpected final answer: %q", answer)
	}

	// trajectory assertions: outcome=success, ≥1 tool_call, ≥2 llm_call steps.
	var outcome string
	var stepCount int
	trajDB.QueryRow(`SELECT outcome, step_count FROM trajectories ORDER BY created_at DESC LIMIT 1`).
		Scan(&outcome, &stepCount)
	if outcome != "success" {
		t.Fatalf("outcome = %q, want success", outcome)
	}
	var toolSteps int
	trajDB.QueryRow(`SELECT COUNT(*) FROM trajectory_steps WHERE step_type='tool_call'`).Scan(&toolSteps)
	if toolSteps < 1 {
		t.Fatal("expected at least one tool_call step")
	}
	var llmSteps int
	trajDB.QueryRow(`SELECT COUNT(*) FROM trajectory_steps WHERE step_type='llm_call'`).Scan(&llmSteps)
	if llmSteps < 2 {
		t.Fatalf("expected >=2 llm_call steps (tool turn + final), got %d", llmSteps)
	}
}

// TestRunner_DedupsRepeatedToolCalls 验证防空转硬护栏：LLM 连发两次**完全相同**的
// tool 调用，框架只真正派发一次（第二次短路，注入上次结果），最终基于结果作答成功。
func TestRunner_DedupsRepeatedToolCalls(t *testing.T) {
	ctx := context.Background()
	schema := loadSchema(t)

	trajDB, err := trajectory.Open(filepath.Join(t.TempDir(), "traj.db"))
	if err != nil {
		t.Fatalf("traj open: %v", err)
	}
	t.Cleanup(func() { trajDB.Close() })
	if err := trajectory.Migrate(trajDB); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var dispatchCount int
	reg := tools.NewRegistry()
	reg.Register("query_distribution", func(c context.Context, args map[string]any) (contract.Response, error) {
		dispatchCount++
		return contract.Response{Status: contract.StatusOK}, nil
	})

	// 同一查询发两次（键序不同，语义相同——验证规范化 key 也能识别），再给最终答案。
	call1 := `{"tool":"query_distribution","args":{"table":"player_basics","column":"level","filter":{"last_online_time":{"op":"<","value":1779499429}}}}`
	call2 := `{"tool":"query_distribution","args":{"column":"level","filter":{"last_online_time":{"value":1779499429,"op":"<"}},"table":"player_basics"}}`
	mock := &seqMock{responses: []string{call1, call2, "最终报告：流失玩家集中在低等级。"}}

	opener := func(c context.Context, ver, q string) (agent.TrajectoryStore, error) {
		return trajectory.New(c, trajDB, ver, q, "benchmark")
	}
	runner := New(mock, reg, opener, schema.Digest())

	answer, err := runner.Run(ctx, "流失玩家卡在哪些等级？")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(answer, "流失") {
		t.Fatalf("unexpected final answer: %q", answer)
	}
	if dispatchCount != 1 {
		t.Fatalf("完全相同的查询应只派发一次，实际派发 %d 次", dispatchCount)
	}

	// trajectory 应只记录 1 个 tool_call 步（第二次被护栏短路、未派发）。
	var toolSteps int
	trajDB.QueryRow(`SELECT COUNT(*) FROM trajectory_steps WHERE step_type='tool_call'`).Scan(&toolSteps)
	if toolSteps != 1 {
		t.Fatalf("expected exactly 1 tool_call step (dup short-circuited), got %d", toolSteps)
	}
}

func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// TestParseToolCall verifies that parseToolCall is tolerant of trailing content
// after the first JSON object (fences, markers, prose) while still rejecting
// pure natural-language answers and stray non-tool objects.
func TestParseToolCall(t *testing.T) {
	type want struct {
		isTool bool
		tool   string // checked only when isTool==true
	}
	cases := []struct {
		name  string
		input string
		want  want
	}{
		{
			name:  "plain one-line JSON",
			input: `{"tool":"query_distribution","args":{"table":"player_currencies"}}`,
			want:  want{isTool: true, tool: "query_distribution"},
		},
		{
			name:  "wrapped in [TOOL_CALL] markers",
			input: "[TOOL_CALL]\n{\"tool\":\"query_distribution\",\"args\":{}}\n[/TOOL_CALL]",
			want:  want{isTool: true, tool: "query_distribution"},
		},
		{
			name:  "wrapped non-strict tool call keys",
			input: "[TOOL_CALL]\n{tool: \"analyze\", args: {\"table\": \"player_basics\", \"aggregates\": [{\"fn\": \"count\", \"as\": \"n\"}]}}\n[/TOOL_CALL]",
			want:  want{isTool: true, tool: "analyze"},
		},
		{
			name: "minimax xml invoke args",
			input: `<minimax:tool_call>
<invoke name="analyze">
<parameter name="args">{"aggregates":[{"as":"n","fn":"count"}],"group_by":["server_id"],"table":"player_basics"}</parameter>
</invoke>
</minimax:tool_call>`,
			want: want{isTool: true, tool: "analyze"},
		},
		{
			name:  "markdown fenced block",
			input: "```json\n{\"tool\":\"query_distribution\",\"args\":{}}\n```",
			want:  want{isTool: true, tool: "query_distribution"},
		},
		{
			name:  "leading prose then JSON",
			input: "我来查询。\n{\"tool\":\"query_distribution\",\"args\":{}}",
			want:  want{isTool: true, tool: "query_distribution"},
		},
		{
			name:  "JSON then trailing prose",
			input: "{\"tool\":\"query_distribution\",\"args\":{}}\n这是我的查询。",
			want:  want{isTool: true, tool: "query_distribution"},
		},
		{
			name:  "pure NL final answer (no brace)",
			input: "## 报告\n头部 0.35% 持有 21.62%",
			want:  want{isTool: false},
		},
		{
			name:  "NL containing a stray non-tool object",
			input: "参考 {\"bucket\":\"0~1w\"} 的数据",
			want:  want{isTool: false},
		},
		{
			name:  "nested args object parses fully",
			input: `{"tool":"query_distribution","args":{"filter":{"currency_type":"coins"},"bucket_key":"coins_balance"}}`,
			want:  want{isTool: true, tool: "query_distribution"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, isTool := parseToolCall(tc.input)
			if isTool != tc.want.isTool {
				t.Fatalf("isTool = %v, want %v (input: %q)", isTool, tc.want.isTool, tc.input)
			}
			if tc.want.isTool && got.Tool != tc.want.tool {
				t.Fatalf("tool = %q, want %q", got.Tool, tc.want.tool)
			}
			// Case 8: additionally verify nested args parsed
			if tc.name == "nested args object parses fully" {
				filter, ok := got.Args["filter"].(map[string]any)
				if !ok {
					t.Fatalf("args[filter] is not a map: %T", got.Args["filter"])
				}
				if filter["currency_type"] != "coins" {
					t.Fatalf("args[filter][currency_type] = %v, want coins", filter["currency_type"])
				}
			}
		})
	}
}

// Verify the llm.NewMock-based substring routing works for standalone use (not the cumulative runner).
func TestMockLLM_SubstringRouting_Standalone(t *testing.T) {
	mock := llm.NewMock(map[string]string{
		"运营问题":         `{"tool":"query_distribution","args":{}}`,
		"player_count": "最终报告：ROI 信号显著。",
	})
	ctx := context.Background()
	// Call with only one matching key at a time — no ambiguity.
	r1, _, _, _, _ := mock.Call(ctx, "运营问题：当前货币分布如何？")
	if !strings.Contains(r1, "query_distribution") {
		t.Fatalf("turn1 routing: %s", r1)
	}
	r2, _, _, _, _ := mock.Call(ctx, `工具返回 [{"player_count":100}]`)
	if !strings.Contains(r2, "ROI") {
		t.Fatalf("turn2 routing: %s", r2)
	}
}

func TestBuildPrompt_Structure(t *testing.T) {
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	got := buildPrompt("SYSTEM", "SCHEMA-DIGEST", now, "运营问题示例")
	expectedNowUnix := now.Unix()
	for _, want := range []string{
		"SYSTEM",
		"SCHEMA-DIGEST",
		"## 当前时间",
		"今天是 2026-05-30",
		fmt.Sprintf("unix=%d", expectedNowUnix),
		"## 运营问题\n运营问题示例",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt missing %q:\n%s", want, got)
		}
	}
	// 预算 cutoff 表：每个常用窗口都有具体 unix 数字
	for _, d := range cutoffWindowsDays {
		want := fmt.Sprintf("%d 日：cutoff = %d", d, expectedNowUnix-int64(d)*86400)
		if !strings.Contains(got, want) {
			t.Fatalf("prompt missing precomputed cutoff %q:\n%s", want, got)
		}
	}
}

func TestBuildPrompt_OmitsEmptySchemaContext(t *testing.T) {
	got := buildPrompt("SYS", "", time.Now(), "Q")
	if strings.Contains(got, "\n\n\n") {
		t.Fatalf("empty schemaContext must not leave triple newline:\n%s", got)
	}
}

// recordingMock 记录每次收到的 conversation prompt，供断言注入内容。
type recordingMock struct {
	responses []string
	idx       int
	prompts   []string
}

func (m *recordingMock) Call(_ context.Context, prompt string) (string, int, int, float64, error) {
	m.prompts = append(m.prompts, prompt)
	if m.idx >= len(m.responses) {
		return "（recordingMock exhausted）", 1, 1, 0, nil
	}
	r := m.responses[m.idx]
	m.idx++
	return r, len(r) / 4, len(r) / 4, 0, nil
}
func (m *recordingMock) Model() string { return "mock-rec" }

var _ llm.Client = (*recordingMock)(nil)

// TestRunner_InjectsResultID_OKOnly：成功结果注入 `结果 id: q{N}`（OK-only 编号），
// 失败结果注入「不计入结果编号」。(b') 修复 B：agent 抄 id 而非数。
func TestRunner_InjectsResultID_OKOnly(t *testing.T) {
	ctx := context.Background()
	schema := loadSchema(t)

	trajDB, err := trajectory.Open(filepath.Join(t.TempDir(), "traj.db"))
	if err != nil {
		t.Fatalf("traj open: %v", err)
	}
	t.Cleanup(func() { trajDB.Close() })
	if err := trajectory.Migrate(trajDB); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// analyze 工具：第 1 次返回 SCHEMA_ERROR（不计入编号），之后返回 OK。
	var analyzeCallCount int
	reg := tools.NewRegistry()
	reg.Register("analyze", func(c context.Context, args map[string]any) (contract.Response, error) {
		analyzeCallCount++
		if analyzeCallCount == 1 {
			return contract.Response{Status: contract.StatusSchemaError, Hint: "列名错"}, nil
		}
		return contract.Response{Status: contract.StatusOK,
			Table: &contract.TableResult{
				Columns: []contract.ColumnMeta{{Name: "n"}}, Rows: [][]any{{42.0}}, RowCount: 1}}, nil
	})

	// 三次 analyze（table 相同、group_by 各异以绕过去重护栏）：fail → ok(q1) → ok(q2)，再最终答案。
	mock := &recordingMock{responses: []string{
		`{"tool":"analyze","args":{"table":"player_basics","group_by":"a"}}`,
		`{"tool":"analyze","args":{"table":"player_basics","group_by":"b"}}`,
		`{"tool":"analyze","args":{"table":"player_basics","group_by":"c"}}`,
		"最终报告：n=42。",
	}}

	opener := func(c context.Context, ver, q string) (agent.TrajectoryStore, error) {
		return trajectory.New(c, trajDB, ver, q, "benchmark")
	}
	runner := New(mock, reg, opener, schema.Digest())
	if _, err := runner.Run(ctx, "测试 q-index 注入"); err != nil {
		t.Fatalf("run: %v", err)
	}

	if analyzeCallCount != 3 {
		t.Fatalf("三次 analyze 应都派发（去重护栏不应吞调用），实际 %d 次", analyzeCallCount)
	}

	last := mock.prompts[len(mock.prompts)-1]
	for _, want := range []string{"结果 id: q1", "结果 id: q2", "不计入结果编号"} {
		if !strings.Contains(last, want) {
			t.Errorf("注入对话缺 %q:\n%s", want, last)
		}
	}
	if strings.Contains(last, "结果 id: q3") {
		t.Errorf("失败调用不应占用编号（不应出现 q3）:\n%s", last)
	}
}
