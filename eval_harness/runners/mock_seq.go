// framework/eval_harness/runners/mock_seq.go
package runners

import (
	"context"
	"fmt"

	"github.com/RuntianLee/schema-driven-insight-agent/llm"
)

// sequencedMock 实现 llm.Client：按调用次序回放 task.llm_turns，不靠子串匹配。
// 解决 llm.NewMock 在多轮累积对话里的歧义 panic（spec §3.1）。
type sequencedMock struct {
	turns []string
	i     int
}

func newSequencedMock(turns []string) *sequencedMock { return &sequencedMock{turns: turns} }

func (m *sequencedMock) Call(_ context.Context, prompt string) (string, int, int, float64, error) {
	if m.i >= len(m.turns) {
		return "", 0, 0, 0, fmt.Errorf(
			"sequencedMock exhausted after %d turns: 任务 llm_turns 未覆盖全部 LLM 轮次（检查 happy-path 假设/SCHEMA_ERROR 自纠）",
			len(m.turns))
	}
	r := m.turns[m.i]
	m.i++
	return r, len(prompt) / 4, len(r) / 4, 0, nil
}

func (m *sequencedMock) Model() string { return "mock-seq" }

// 编译期断言：满足 llm.Client。
var _ llm.Client = (*sequencedMock)(nil)
