// Command eino-probe 一次性可行性探针（Eino Runner 接管 Phase-0 闸）。
//
// 与 cmd/gateway-tools-probe（裸 HTTP）的关键区别：本探针真用 Eino 的 claude
// ChatModel + schema.ToolInfo + Generate——编译通过即证「Eino API 面与现实吻合」，
// 活体运行（单次、串行、key 取环境变量）才判网关可达 + 原生 tool_use 透传。
//
// 已确定（无需本探针）：Eino 支持 anthropic 格式（eino-ext claude 底层即官方
// anthropics/anthropic-sdk-go）。本探针验收窄后的真未知：这条具体链路端到端
// 能否吐/收结构化 tool_use（BaseURL 拼接 / okaoi×MiniMax 经 SDK 的响应解析 /
// anthropic-version 头）。
//
// 绝不并发、绝不循环。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/cloudwego/eino-ext/components/model/claude"
	"github.com/cloudwego/eino/schema"
)

func main() {
	// 读配置：endpoint/model 取环境变量（含默认），api_key 必须由环境变量提供——无 secret 入库。
	// BaseURL 填基址（不含 /v1/messages），SDK 内部追加路径。
	baseURL := getEnv("EINO_PROBE_BASE_URL", "https://www.okaoi.com")
	apiKey := os.Getenv("EINO_PROBE_API_KEY")
	model := getEnv("EINO_PROBE_MODEL", "MiniMax-M2.7")

	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "错误：请设置 EINO_PROBE_API_KEY 环境变量（okaoi api key）")
		os.Exit(1)
	}

	if err := run(context.Background(), baseURL, apiKey, model); err != nil {
		fmt.Fprintf(os.Stderr, "探针失败: %v\n", err)
		os.Exit(1)
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// run 执行探针：装配 claude ChatModel → 绑 analyze tool → Generate（带 tool）→
// 检视 ToolCalls；再跑对照组（不绑 tool）。全程串行、单次。
func run(ctx context.Context, baseURL, apiKey, model string) error {
	cm, err := claude.NewChatModel(ctx, &claude.Config{
		APIKey:    apiKey,
		Model:     model,
		MaxTokens: 512,
		BaseURL:   &baseURL,
		// 若 okaoi 拒默认 anthropic-version，解开下行兜底：
		// AdditionalHeaderFields: map[string]string{"anthropic-version": "2023-06-01"},
	})
	if err != nil {
		return fmt.Errorf("NewChatModel: %w", err)
	}

	analyzeTool := &schema.ToolInfo{
		Name: "analyze",
		Desc: "查询数据库表并返回分析结果",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"table": {
				Type:     schema.String,
				Desc:     "要查询的表名",
				Required: true,
			},
		}),
	}

	userMsg := "请查询 player_basics 表，告诉我里面有哪些列。"

	// ── A：绑 tool ───────────────────────────────────────────────────────────
	fmt.Println("=== A (with tool) ===")
	fmt.Printf("base_url: %s\nmodel: %s\n", baseURL, model)
	toolModel, err := cm.WithTools([]*schema.ToolInfo{analyzeTool})
	if err != nil {
		return fmt.Errorf("WithTools: %w", err)
	}
	t0 := time.Now()
	respA, errA := toolModel.Generate(ctx, []*schema.Message{schema.UserMessage(userMsg)})
	elapsedA := time.Since(t0)
	if errA != nil {
		fmt.Printf("Generate(A) 失败 (elapsed=%s): %v\n仪器有效性: 不可达/错误，不能解读为「Eino 不支持」\n", elapsedA, errA)
		return errA
	}
	reportResult("A", respA, elapsedA)

	// ── B：对照组（不绑 tool）──────────────────────────────────────────────
	fmt.Println("\n=== B (control, no tool) ===")
	t1 := time.Now()
	respB, errB := cm.Generate(ctx, []*schema.Message{schema.UserMessage(userMsg)})
	elapsedB := time.Since(t1)
	if errB != nil {
		fmt.Printf("Generate(B) 失败 (elapsed=%s): %v\n", elapsedB, errB)
		return errB
	}
	reportResult("B", respB, elapsedB)

	return nil
}

// reportResult 打印一次 Generate 结果 + 判绿（仪器校验：empty content / tool_use / 填参）。
func reportResult(label string, resp *schema.Message, elapsed time.Duration) {
	fmt.Printf("[%s] elapsed=%s  role=%s  tool_calls=%d\n", label, elapsed, resp.Role, len(resp.ToolCalls))
	if resp.Content == "" && len(resp.ToolCalls) == 0 {
		fmt.Printf("  ⚠ 空响应（无 content 无 tool_calls）——仪器存疑，核对 endpoint/key\n")
	}
	if resp.Content != "" {
		preview := resp.Content
		if len(preview) > 300 {
			preview = preview[:300] + "…"
		}
		fmt.Printf("  content: %s\n", preview)
	}
	for i, tc := range resp.ToolCalls {
		fmt.Printf("  tool_call[%d]: name=%s args=%s\n", i, tc.Function.Name, tc.Function.Arguments)
		// 判绿：name 对 + Arguments JSON 含 table。
		var args map[string]any
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			fmt.Printf("    ✗ Arguments 非合法 JSON: %v\n", err)
			continue
		}
		if tc.Function.Name == "analyze" {
			if v, ok := args["table"]; ok {
				fmt.Printf("    ✓ 判绿：原生 tool_use 正确填参 table=%v\n", v)
			} else {
				fmt.Printf("    ✗ tool_use 缺 table 参数\n")
			}
		}
	}
}
