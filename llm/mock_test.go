package llm

import (
	"context"
	"testing"
)

func TestMockSubstringRouting(t *testing.T) {
	m := NewMock(map[string]string{
		"虚拟货币分布":       `{"tool":"query_distribution"}`,
		"player_count": "最终报告：集中度信号显著。",
	})
	ctx := context.Background()

	r1, _, _, _, _ := m.Call(ctx, "用户问题：当前虚拟货币分布如何？")
	if r1 != `{"tool":"query_distribution"}` {
		t.Fatalf("turn1 routing failed: %s", r1)
	}
	r2, _, _, _, _ := m.Call(ctx, `tool 返回 [{"player_count": 100}]`)
	if r2 != "最终报告：集中度信号显著。" {
		t.Fatalf("turn2 routing failed: %s", r2)
	}
	if m.Model() != "mock" {
		t.Fatal("model name")
	}
}
