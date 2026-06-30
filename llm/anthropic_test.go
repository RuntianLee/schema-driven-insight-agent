package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newAnthropicTestClient 复用内部构造器，format=anthropic。
func newAnthropicTestClient(url string, maxTokens int) Client {
	return newMiniMaxFull("secret-key", "claude-x", url, 5*time.Second, maxTokens, nil, "anthropic")
}

func TestAnthropicCall_HappyPath(t *testing.T) {
	var gotHeaders http.Header
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		gotBody, _ = io.ReadAll(r.Body)
		w.Write([]byte(`{"type":"message","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":5}}`))
	}))
	defer srv.Close()

	c := newAnthropicTestClient(srv.URL, 100)
	got, tokIn, tokOut, cost, err := c.Call(context.Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "hello" {
		t.Fatalf("resp = %q, want %q", got, "hello")
	}
	if tokIn != 3 || tokOut != 5 {
		t.Fatalf("tok = (%d,%d), want (3,5)", tokIn, tokOut)
	}
	wantCost := float64(3)/1000*costPerKTokenIn + float64(5)/1000*costPerKTokenOut
	if cost != wantCost {
		t.Fatalf("cost = %v, want %v", cost, wantCost)
	}

	// 头校验：x-api-key + anthropic-version；不发 Authorization Bearer。
	if gotHeaders.Get("x-api-key") != "secret-key" {
		t.Fatalf("x-api-key = %q, want secret-key", gotHeaders.Get("x-api-key"))
	}
	if gotHeaders.Get("anthropic-version") != "2023-06-01" {
		t.Fatalf("anthropic-version = %q, want 2023-06-01", gotHeaders.Get("anthropic-version"))
	}
	if gotHeaders.Get("Authorization") != "" {
		t.Fatalf("Authorization 应为空（Anthropic 不发 Bearer），得到 %q", gotHeaders.Get("Authorization"))
	}
	if !strings.Contains(gotHeaders.Get("content-type"), "application/json") {
		t.Fatalf("content-type = %q, want application/json", gotHeaders.Get("content-type"))
	}

	// body 形状：max_tokens>0、messages 含 user。
	var req map[string]any
	if err := json.Unmarshal(gotBody, &req); err != nil {
		t.Fatalf("body not json: %v; body=%s", err, gotBody)
	}
	if mt, ok := req["max_tokens"].(float64); !ok || mt <= 0 {
		t.Fatalf("max_tokens 应为正数，得到 %v", req["max_tokens"])
	}
	msgs, ok := req["messages"].([]any)
	if !ok || len(msgs) != 1 {
		t.Fatalf("messages 应为长度1数组，得到 %v", req["messages"])
	}
}

func TestAnthropicCall_DefaultMaxTokens(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer srv.Close()

	c := newAnthropicTestClient(srv.URL, 0) // 0 → 默认 4096
	if _, _, _, _, err := c.Call(context.Background(), "hi"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var req map[string]any
	if err := json.Unmarshal(gotBody, &req); err != nil {
		t.Fatalf("body not json: %v", err)
	}
	if mt, _ := req["max_tokens"].(float64); int(mt) != 4096 {
		t.Fatalf("默认 max_tokens 应为 4096，得到 %v", req["max_tokens"])
	}
}

func TestAnthropicCall_EmptyContentSurfacesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"content":[],"usage":{"input_tokens":2,"output_tokens":0}}`))
	}))
	defer srv.Close()

	c := newAnthropicTestClient(srv.URL, 100)
	_, _, _, _, err := c.Call(context.Background(), "hi")
	if err == nil {
		t.Fatal("空 content 应返回可诊断错误")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Fatalf("错误须含 'empty' 诊断，得到: %v", err)
	}
}

func TestAnthropicCall_MultiTextBlocksConcatenated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"content":[{"type":"thinking","text":"思考忽略"},{"type":"text","text":"foo"},{"type":"text","text":"bar"}],"usage":{"input_tokens":1,"output_tokens":2}}`))
	}))
	defer srv.Close()

	c := newAnthropicTestClient(srv.URL, 100)
	got, _, _, _, err := c.Call(context.Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "foobar" {
		t.Fatalf("应拼接 text 块（忽略 thinking），得到 %q，want foobar", got)
	}
}

func TestAnthropicCall_ErrorTypeResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"bad request detail"}}`))
	}))
	defer srv.Close()

	c := newAnthropicTestClient(srv.URL, 100)
	_, _, _, _, err := c.Call(context.Background(), "hi")
	if err == nil {
		t.Fatal("type==error 应返回错误")
	}
	if !strings.Contains(err.Error(), "bad request detail") {
		t.Fatalf("错误须含网关 message，得到: %v", err)
	}
}

func TestAnthropicCall_Non2xxSurfacesBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(`<html>gateway error</html>`))
	}))
	defer srv.Close()

	c := newAnthropicTestClient(srv.URL, 100)
	_, _, _, _, err := c.Call(context.Background(), "hi")
	if err == nil {
		t.Fatal("非 2xx 应返回错误")
	}
	if !strings.Contains(err.Error(), "gateway error") {
		t.Fatalf("错误须含原始 body，得到: %v", err)
	}
}

func TestAnthropicCall_ThinkingDisabledSerialized(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Write([]byte(`{"type":"message","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer srv.Close()

	base := newMiniMaxFull("k", "m", srv.URL, 5*time.Second, 2000, nil, "anthropic").(*minimaxClient)
	judge := base.WithJudgeProfile(8000, true)
	if _, _, _, _, err := judge.Call(context.Background(), "hi"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var req map[string]any
	if err := json.Unmarshal(gotBody, &req); err != nil {
		t.Fatalf("body 非 JSON: %v", err)
	}
	if req["max_tokens"] != float64(8000) {
		t.Fatalf("max_tokens = %v, want 8000", req["max_tokens"])
	}
	th, ok := req["thinking"].(map[string]any)
	if !ok || th["type"] != "disabled" {
		t.Fatalf("thinking 应为 {type:disabled}，得 %v", req["thinking"])
	}
}

func TestAnthropicCall_ThinkingOmittedByDefault(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Write([]byte(`{"type":"message","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer srv.Close()

	c := newAnthropicTestClient(srv.URL, 8000)
	if _, _, _, _, err := c.Call(context.Background(), "hi"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var req map[string]any
	json.Unmarshal(gotBody, &req)
	if _, present := req["thinking"]; present {
		t.Fatalf("disableThinking=false 时不应发 thinking 字段，得 %v", req["thinking"])
	}
}

func TestAnthropicCall_ThinkingAteBudgetDiagnostic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"type":"message","content":[{"type":"thinking","text":"推理中..."}],"stop_reason":"max_tokens","usage":{"input_tokens":5,"output_tokens":100}}`))
	}))
	defer srv.Close()

	c := newAnthropicTestClient(srv.URL, 100)
	_, _, _, _, err := c.Call(context.Background(), "hi")
	if err == nil {
		t.Fatal("应报错（空 text 块）")
	}
	if !strings.Contains(err.Error(), "thinking") || !strings.Contains(err.Error(), "max_tokens") {
		t.Fatalf("诊断文案应点名 thinking + max_tokens，得: %v", err)
	}
}

// --- config 层 ---

func TestParseMiniMaxConfig_FormatDefaultOpenAI(t *testing.T) {
	cfg, err := ParseMiniMaxConfig([]byte("model: x\n"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Format != "" {
		t.Fatalf("未设 format 应保持空（向后兼容，视为 openai），得到 %q", cfg.Format)
	}
}

func TestParseMiniMaxConfig_FormatAnthropic(t *testing.T) {
	cfg, err := ParseMiniMaxConfig([]byte("format: anthropic\n"))
	if err != nil {
		t.Fatalf("format=anthropic 应解析成功: %v", err)
	}
	if cfg.Format != "anthropic" {
		t.Fatalf("Format = %q, want anthropic", cfg.Format)
	}
}

func TestParseMiniMaxConfig_FormatBogusRejected(t *testing.T) {
	_, err := ParseMiniMaxConfig([]byte("format: bogus\n"))
	if err == nil {
		t.Fatal("非法 format 应报错")
	}
	if !strings.Contains(err.Error(), "format") {
		t.Fatalf("错误须提及 format，得到: %v", err)
	}
}
