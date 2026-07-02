package eino_agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino-ext/components/model/claude"
	"github.com/cloudwego/eino/components/model"

	"github.com/RuntianLee/schema-driven-insight-agent/llm"
)

// anthropicDefaultMaxTokens：cfg 未设 max_tokens 时的默认上限（须 >0）。
const anthropicDefaultMaxTokens = 4096

// NewChatModel 从 MiniMaxConfig 建 Agent 腿的 claude ChatModel（anthropic wire / 网关）。
// Agent 腿要求 anthropic wire；openai 格式延后，此处拒绝。
func NewChatModel(ctx context.Context, cfg llm.MiniMaxConfig) (model.ToolCallingChatModel, error) {
	if cfg.Format != "anthropic" {
		return nil, fmt.Errorf("Agent 腿要求 anthropic wire，config format=%q（openai parity 延后）", cfg.Format)
	}
	key, err := cfg.ResolveAPIKey()
	if err != nil {
		return nil, err
	}
	maxTok := cfg.MaxTokens
	if maxTok <= 0 {
		maxTok = anthropicDefaultMaxTokens
	}
	base := baseURLFromEndpoint(cfg.Endpoint)
	cm, err := claude.NewChatModel(ctx, &claude.Config{
		APIKey:    key,
		Model:     cfg.Model,
		MaxTokens: maxTok,
		BaseURL:   &base,
	})
	if err != nil {
		return nil, fmt.Errorf("claude.NewChatModel: %w", err)
	}
	return cm, nil
}

// baseURLFromEndpoint 从可能是全路径的 endpoint 剥出 base URL（去掉尾部 /v1/messages 与斜杠）。
// 生产 config 的 endpoint 是全 messages URL（judge 腿裸 HTTP 直用）；claude SDK 会再追加
// /v1/messages，故 agent 腿需先剥回基址，避免双重路径。
func baseURLFromEndpoint(endpoint string) string {
	e := strings.TrimRight(endpoint, "/")
	e = strings.TrimSuffix(e, "/v1/messages")
	return strings.TrimRight(e, "/")
}
