package eino_agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RuntianLee/schema-driven-insight-agent/agent"
	"github.com/RuntianLee/schema-driven-insight-agent/contract"
	"github.com/RuntianLee/schema-driven-insight-agent/llm"
	"github.com/RuntianLee/schema-driven-insight-agent/tools"
	"github.com/RuntianLee/schema-driven-insight-agent/trajectory"
)

// capturingMock 是 llm.Client 的 mock：按序返回 responses，并记录每次收到的 conversation
// （prompts），便于断言工具响应被回灌进下一轮对话的内容。
type capturingMock struct {
	responses []string
	idx       int
	prompts   []string
}

func (m *capturingMock) Call(_ context.Context, prompt string) (string, int, int, float64, error) {
	m.prompts = append(m.prompts, prompt)
	if m.idx >= len(m.responses) {
		return "（capturingMock exhausted）", 1, 1, 0, nil
	}
	r := m.responses[m.idx]
	m.idx++
	return r, len(r) / 4, len(r) / 4, 0, nil
}
func (m *capturingMock) Model() string { return "capturing-mock" }

var _ llm.Client = (*capturingMock)(nil)

// TestRunner_FeedsSchemaErrorHintBackToLLM 确定性证明「未知参数键自修闭环第 2 段：提示送达」。
// 即：当 analyze 收到 filter(单数) 返回 SCHEMA_ERROR + did-you-mean（经真 tools.ParseAnalyzeArgs），
// runner 会把该 hint **原样回灌**进下一轮喂给 LLM 的 conversation——LLM 因而一定能看到「did you mean
// "filters"」，具备自修条件。本测试只验「提示是否送达 LLM」（确定性），不验 LLM 是否真自修（属真 LLM 探针）。
func TestRunner_FeedsSchemaErrorHintBackToLLM(t *testing.T) {
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

	// analyze handler 走真 tools.ParseAnalyzeArgs：filter(单数) → 真 SCHEMA_ERROR + did-you-mean。
	reg := tools.NewRegistry()
	reg.Register("analyze", func(_ context.Context, args map[string]any) (contract.Response, error) {
		in, errResp := tools.ParseAnalyzeArgs(args)
		if errResp != nil {
			return *errResp, nil
		}
		_ = in
		return contract.Response{Status: contract.StatusOK}, nil
	})

	mock := &capturingMock{
		responses: []string{
			// turn 1: analyze 用 filter(单数)——footgun，触发真 SCHEMA_ERROR。
			`{"tool":"analyze","args":{"table":"customers","filter":{"field":"Balance","op":">","value":150000}}}`,
			// turn 2: 给最终回答结束循环（本测试只验 hint 送达，不验 LLM 是否自修）。
			"最终回答（占位）。",
		},
	}
	opener := func(c context.Context, ver, q string) (agent.TrajectoryStore, error) {
		return trajectory.New(c, trajDB, ver, q, "benchmark")
	}
	runner := New(mock, reg, opener, schema.Digest())

	if _, err := runner.Run(ctx, "各地域高净值客户数？"); err != nil {
		t.Fatalf("run: %v", err)
	}

	// 第 2 次 LLM 调用收到的 conversation 必含工具返回的 SCHEMA_ERROR + did-you-mean hint。
	if len(mock.prompts) < 2 {
		t.Fatalf("expected ≥2 LLM calls (tool turn + answer turn), got %d", len(mock.prompts))
	}
	second := mock.prompts[1]
	if !strings.Contains(second, "SCHEMA_ERROR") {
		t.Fatalf("2nd-turn conversation must carry SCHEMA_ERROR status; got:\n%s", second)
	}
	// 注：hint 经 json.Marshal 后引号被转义为 \"，故按不含引号的 token 断言（更稳）。
	for _, want := range []string{"unknown arg key", "filter", "did you mean", "filters"} {
		if !strings.Contains(second, want) {
			t.Fatalf("2nd-turn conversation must carry did-you-mean token %q; got:\n%s", want, second)
		}
	}
}
