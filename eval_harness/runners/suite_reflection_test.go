// framework/eval_harness/runners/suite_reflection_test.go
package runners

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// recordingProvider 记录被调用并返回固定上下文。
type recordingProvider struct{ calls int }

func (p *recordingProvider) ContextFor(_ context.Context, taskID, _ string) (string, error) {
	p.calls++
	return "REFLECTION_CONTEXT_FOR:" + taskID, nil
}

func TestApplyReflection_NilProvider_ReturnsQuestionUnchanged(t *testing.T) {
	got := applyReflection(context.Background(), nil, "t1", "原问题")
	if got != "原问题" {
		t.Fatalf("nil provider 应原样返回 question，得 %q", got)
	}
}

func TestApplyReflection_WithProvider_PrependsContext(t *testing.T) {
	p := &recordingProvider{}
	got := applyReflection(context.Background(), p, "t1", "原问题")
	if p.calls != 1 {
		t.Fatalf("provider 应被调用 1 次，得 %d", p.calls)
	}
	if !strings.Contains(got, "REFLECTION_CONTEXT_FOR:t1") || !strings.HasSuffix(got, "原问题") {
		t.Fatalf("应前置 reflection 上下文并保留原问题，得 %q", got)
	}
	if !strings.Contains(got, "\n\n---\n\n") {
		t.Fatalf("应用 \\n\\n---\\n\\n 分隔 reflection 上下文与原问题，得 %q", got)
	}
}

// emptyProvider 返回空串：应等价于无注入（spec：返回空 → 原样返回 question）。
type emptyProvider struct{}

func (emptyProvider) ContextFor(_ context.Context, _, _ string) (string, error) { return "", nil }

// errProvider 返回 error：应等价于无注入（spec：err → 原样返回 question）。
type errProvider struct{}

func (errProvider) ContextFor(_ context.Context, _, _ string) (string, error) {
	return "应被忽略", errors.New("provider 失败")
}

func TestApplyReflection_EmptyContext_ReturnsQuestionUnchanged(t *testing.T) {
	got := applyReflection(context.Background(), emptyProvider{}, "t1", "原问题")
	if got != "原问题" {
		t.Fatalf("空 reflection 上下文应原样返回 question，得 %q", got)
	}
}

func TestApplyReflection_ProviderError_ReturnsQuestionUnchanged(t *testing.T) {
	got := applyReflection(context.Background(), errProvider{}, "t1", "原问题")
	if got != "原问题" {
		t.Fatalf("provider 报错应原样返回 question（含丢弃错误返回的串），得 %q", got)
	}
}
