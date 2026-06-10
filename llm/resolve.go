package llm

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
)

// Resolve 按优先级加载 LLM client：config 文件 > env(MINIMAX_API_KEY) > stateless mock。
//
//   - config 存在且合法            → 用 config 构造 MiniMax client；解析/构造失败 → error。
//   - config 不存在(IsNotExist) 且 MINIMAX_API_KEY 已设 → 用 env 构造 MiniMax client。
//   - config 不存在 且 无 env key   → stateless mock（nil error）。
//   - config 存在但不可读(非 IsNotExist) → error。
//
// API key 绝不打印。
func Resolve(configPath string) (Client, error) { return resolve(configPath, true) }

// ResolveStrict 与 Resolve 相同，但在「无 config 且无 env key」时返回 error 而非退回
// stateless mock。需要真 LLM 的场景（如 eval 真评测道）用它：静默 mock 会让「测真 LLM」
// 偷偷跑成 mock —— 违反诚实纪律。
func ResolveStrict(configPath string) (Client, error) { return resolve(configPath, false) }

func resolve(configPath string, allowMock bool) (Client, error) {
	data, err := os.ReadFile(configPath)
	if err == nil {
		cfg, err := ParseMiniMaxConfig(data)
		if err != nil {
			return nil, fmt.Errorf("解析 %s 失败: %w", configPath, err)
		}
		client, err := NewMiniMaxFromConfig(cfg)
		if err != nil {
			return nil, fmt.Errorf("%w\n提示：请参考 config/llm.example.yaml 创建 %s", err, configPath)
		}
		fmt.Fprintf(os.Stderr, "LLM: endpoint=%s model=%s (config)\n", cfg.Endpoint, cfg.Model)
		return client, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("读取 %s 失败: %w", configPath, err)
	}
	if apiKey := os.Getenv("MINIMAX_API_KEY"); apiKey != "" {
		model := os.Getenv("MINIMAX_MODEL")
		if model == "" {
			model = "MiniMax-M2.7"
		}
		fmt.Fprintf(os.Stderr, "LLM: model=%s (env)\n", model)
		return NewMiniMax(apiKey, model), nil
	}
	if !allowMock {
		return nil, fmt.Errorf("未找到真 LLM 配置：%s 不存在且 MINIMAX_API_KEY 未设置（真评测道需真 LLM；请配 config/llm.yaml 或设 MINIMAX_API_KEY）", configPath)
	}
	log.Println("警告：MINIMAX_API_KEY 未设置，且未找到 config 文件，使用 stateless mock LLM。")
	return &statelessMockClient{}, nil
}

// ── 本地 stateless mock（从 cmd/agent 迁入）────────────────────────────────────
//
// 不使用 llm.NewMock：system prompt 本身含 "player_count"，多轮对话后 scripted map
// 会命中 >1 key，触发 NewMock 的歧义 panic。此 stateless mock 按对话阶段路由，REPL 多轮安全。
//
// 检测前缀 "## 工具 " 以覆盖任意工具名（对应 eino_agent/runner.go 的工具结果标记）。
type statelessMockClient struct{}

const (
	toolResultMarker = "## 工具 "
	mockFinalAnswer  = "（mock）分布已生成，请配置 MINIMAX_API_KEY 获取真实洞察。"
	mockToolCallJSON = `{"tool":"query_distribution","args":{"table":"player_currencies","column":"balance","bucket_key":"coins_balance","filter":{"currency_type":"coins"}}}`
)

func (m *statelessMockClient) Call(_ context.Context, prompt string) (string, int, int, float64, error) {
	if strings.Contains(prompt, toolResultMarker) {
		return mockFinalAnswer, len(prompt) / 4, len(mockFinalAnswer) / 4, 0, nil
	}
	return mockToolCallJSON, len(prompt) / 4, len(mockToolCallJSON) / 4, 0, nil
}

func (m *statelessMockClient) Model() string { return "mock" }

var _ Client = (*statelessMockClient)(nil)
