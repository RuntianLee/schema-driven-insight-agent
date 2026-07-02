// Command gateway-tools-probe 一次性调研探针：测试 anthropic 兼容网关
// 是否支持原生 tools 透传（请求带 tools 字段，响应能否回结构化 tool_use 块）。
//
// 串行发 2 个请求（A 带 tools / B 不带），打印原始响应用于人工核验。
// 绝不并发、绝不循环。
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// ── 请求/响应结构（Anthropic Messages API wire 格式）────────────────────────

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type inputSchema struct {
	Type       string              `json:"type"`
	Properties map[string]property `json:"properties"`
	Required   []string            `json:"required,omitempty"`
}

type property struct {
	Type        string `json:"type"`
	Description string `json:"description"`
}

type tool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema inputSchema `json:"input_schema"`
}

type requestBody struct {
	Model     string    `json:"model"`
	MaxTokens int       `json:"max_tokens"`
	Messages  []message `json:"messages"`
	Tools     []tool    `json:"tools,omitempty"`
}

func main() {
	// ── 读配置（与生产代码相同的来源）────────────────────────────────────────
	// endpoint/model 默认值取自环境变量（PROBE_ENDPOINT / PROBE_MODEL），api_key 必须由
	// PROBE_API_KEY 提供——无任何 secret 入库；endpoint 同 config/llm.yaml 的网关配置。
	endpoint := getEnv("PROBE_ENDPOINT", "")
	apiKey := getEnv("PROBE_API_KEY", "")
	model := getEnv("PROBE_MODEL", "MiniMax-M2.7")

	if endpoint == "" {
		fmt.Fprintln(os.Stderr, "错误：请设置 PROBE_ENDPOINT 环境变量（anthropic 兼容网关地址）")
		os.Exit(1)
	}

	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "错误：请设置 PROBE_API_KEY 环境变量（网关 api key）")
		os.Exit(1)
	}

	client := &http.Client{Timeout: 30 * time.Second}

	userMsg := "请查询 player_basics 表，告诉我里面有哪些列。"

	// ── 请求 A：带 tools 字段 ────────────────────────────────────────────────
	fmt.Println("\n=== A (with tools) ===")
	bodyA := requestBody{
		Model:     model,
		MaxTokens: 512,
		Messages:  []message{{Role: "user", Content: userMsg}},
		Tools: []tool{
			{
				Name:        "analyze",
				Description: "查询数据库表并返回分析结果",
				InputSchema: inputSchema{
					Type: "object",
					Properties: map[string]property{
						"table": {
							Type:        "string",
							Description: "要查询的表名",
						},
					},
					Required: []string{"table"},
				},
			},
		},
	}
	sendAndPrint(client, endpoint, apiKey, bodyA)

	// ── 请求 B：不带 tools（对照组）─────────────────────────────────────────
	fmt.Println("\n=== B (control, no tools) ===")
	bodyB := requestBody{
		Model:     model,
		MaxTokens: 512,
		Messages:  []message{{Role: "user", Content: userMsg}},
	}
	sendAndPrint(client, endpoint, apiKey, bodyB)
}

// sendAndPrint 发单个请求并打印 HTTP 状态 + 原始 body（前 600 字节足够判断结构）。
func sendAndPrint(client *http.Client, endpoint, apiKey string, body requestBody) {
	startedAt := time.Now()
	raw, err := json.Marshal(body)
	if err != nil {
		fmt.Printf("marshal 失败: %v\n", err)
		return
	}

	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		fmt.Printf("build request 失败: %v\n", err)
		return
	}
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	fmt.Printf("endpoint: %s\nmodel: %s\n", endpoint, body.Model)
	fmt.Printf("has_tools: %v\n", len(body.Tools) > 0)

	resp, err := client.Do(req)
	elapsed := time.Since(startedAt)
	if err != nil {
		fmt.Printf("HTTP 请求失败 (elapsed=%s): %v\n仪器有效性: 不可达/超时，不能解读为「网关不支持」\n", elapsed, err)
		return
	}
	defer resp.Body.Close()

	respBytes, _ := io.ReadAll(resp.Body)
	fmt.Printf("HTTP 状态: %d  耗时: %s\n", resp.StatusCode, elapsed)

	// 打印前 600 字节（够看 type/stop_reason/tool_use 等关键字段）
	preview := respBytes
	truncated := false
	if len(preview) > 600 {
		preview = preview[:600]
		truncated = true
	}
	fmt.Printf("响应 body（前%d字节）:\n%s\n", len(preview), string(preview))
	if truncated {
		fmt.Printf("... [截断，原始长度 %d 字节]\n", len(respBytes))
	}

	// 检测关键字段
	bodyStr := string(respBytes)
	checkContains(bodyStr, `"tool_use"`, "检测到 tool_use 块")
	checkContains(bodyStr, `"tool_calls"`, "检测到 tool_calls 字段")
	checkContains(bodyStr, `"stop_reason":"tool_use"`, "stop_reason = tool_use")
	checkContains(bodyStr, `"stop_reason": "tool_use"`, "stop_reason = tool_use（空格）")
}

func checkContains(body, needle, label string) {
	if bytes.Contains([]byte(body), []byte(needle)) {
		fmt.Printf("  ✓ %s\n", label)
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
