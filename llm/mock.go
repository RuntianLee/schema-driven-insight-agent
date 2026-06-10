package llm

import (
	"context"
	"strings"
)

// mockClient：scripted 的 key 是 prompt 的子串，命中则返回对应 value（确定性）。
// 顺序无关——靠 prompt 内容（首轮含问题、次轮含 tool 结果）区分轮次。
type mockClient struct {
	scripted map[string]string
	fallback string
}

// NewMock 构造可脚本化的 mock LLM（I4 / E2E 用）。
func NewMock(scripted map[string]string) Client {
	return &mockClient{scripted: scripted, fallback: "（mock 无匹配脚本）"}
}

func (m *mockClient) Call(_ context.Context, prompt string) (string, int, int, float64, error) {
	var matchedKey, matchedResp string
	matches := 0
	for key, resp := range m.scripted {
		if strings.Contains(prompt, key) {
			matches++
			matchedKey, matchedResp = key, resp
		}
	}
	switch {
	case matches > 1:
		panic("mock: ambiguous prompt matches multiple scripted keys — fix test scripting")
	case matches == 1:
		_ = matchedKey
		return matchedResp, len(prompt) / 4, len(matchedResp) / 4, 0, nil
	default:
		return m.fallback, len(prompt) / 4, len(m.fallback) / 4, 0, nil
	}
}

func (m *mockClient) Model() string { return "mock" }
