package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewMiniMaxModelDefault(t *testing.T) {
	c := NewMiniMax("fake-key", "")
	if c.Model() != "MiniMax-M2.7" {
		t.Fatalf("default model = %q, want \"MiniMax-M2.7\"", c.Model())
	}
	if NewMiniMax("k", "custom").Model() != "custom" {
		t.Fatal("explicit model override failed")
	}
}

func TestMiniMaxCall_EmptyContentSurfacesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":""}}],"usage":{"prompt_tokens":10,"completion_tokens":0},"base_resp":{"status_code":0,"status_msg":"success"}}`))
	}))
	defer srv.Close()
	c := newMiniMaxFull("k", "MiniMax-M2.7", srv.URL, 5*time.Second, 0, nil)
	_, _, _, _, err := c.Call(context.Background(), "hi")
	if err == nil {
		t.Fatal("空 content 应返回错误（可诊断），而非静默空串")
	}
	if !strings.Contains(err.Error(), "empty content") {
		t.Fatalf("错误须含 'empty content' 诊断，得到: %v", err)
	}
}

func TestMiniMaxCall_ReasoningContentFallback(t *testing.T) {
	// 领先假设：推理模型把答案放 reasoning_content、content 空。回退取 reasoning_content。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"","reasoning_content":"[\"主张A\"]"}}],"usage":{"prompt_tokens":10,"completion_tokens":5},"base_resp":{"status_code":0}}`))
	}))
	defer srv.Close()
	c := newMiniMaxFull("k", "MiniMax-M2.7", srv.URL, 5*time.Second, 0, nil)
	got, _, _, _, err := c.Call(context.Background(), "hi")
	if err != nil {
		t.Fatalf("有 reasoning_content 时应回退取用、不报错: %v", err)
	}
	if !strings.Contains(got, "主张A") {
		t.Fatalf("应回退 reasoning_content，得到: %q", got)
	}
}
