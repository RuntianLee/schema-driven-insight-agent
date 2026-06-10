// framework/eval_harness/evaluators/mockjudge.go
package evaluators

import (
	"context"

	"github.com/RuntianLee/schema-driven-insight-agent/llm"
)

// mockJudge 是 MVP 的 judge client：无视 prompt，返回固定占位分（诚实优先，spec §4.3）。
type mockJudge struct{ resp string }

// NewMockJudge 返回恒定 score=3 的 mock judge client。
func NewMockJudge() llm.Client { return &mockJudge{resp: `{"score":3,"reason":"[mock] 管道连通"}`} }

// constJudge 返回固定任意字符串的 client（测试 R3 解析容错用）。
func constJudge(s string) llm.Client { return &mockJudge{resp: s} }

func (m *mockJudge) Call(_ context.Context, _ string) (string, int, int, float64, error) {
	return m.resp, 0, 0, 0, nil
}
func (m *mockJudge) Model() string { return "mock-judge" }

var _ llm.Client = (*mockJudge)(nil)
