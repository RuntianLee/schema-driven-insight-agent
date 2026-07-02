package eino_agent

import (
	"context"
	"testing"

	"github.com/RuntianLee/schema-driven-insight-agent/llm"
)

func TestNewChatModel_RequiresAnthropicWire(t *testing.T) {
	_, err := NewChatModel(context.Background(), llm.MiniMaxConfig{Format: "openai", APIKey: "x", Model: "m"})
	if err == nil {
		t.Fatal("openai wire 应被拒（Agent 腿要求 anthropic）")
	}
}

func TestNewChatModel_BuildsBoundModel(t *testing.T) {
	cm, err := NewChatModel(context.Background(), llm.MiniMaxConfig{
		Format: "anthropic", APIKey: "dummy", Model: "MiniMax-M2.7",
		Endpoint: "https://gateway.example.com", MaxTokens: 4096,
	})
	if err != nil {
		t.Fatalf("NewChatModel: %v", err)
	}
	if cm == nil {
		t.Fatal("nil model")
	}
	bound, err := cm.WithTools(ToolInfos())
	if err != nil || bound == nil {
		t.Fatalf("WithTools: %v", err)
	}
}

func TestBaseURLFromEndpoint(t *testing.T) {
	cases := map[string]string{
		"https://gateway.example.com/v1/messages":  "https://gateway.example.com",
		"https://gateway.example.com/v1/messages/": "https://gateway.example.com",
		"https://gateway.example.com":              "https://gateway.example.com",
		"https://gateway.example.com/":             "https://gateway.example.com",
		"https://api.example.com/base/v1/messages": "https://api.example.com/base",
	}
	for in, want := range cases {
		if got := baseURLFromEndpoint(in); got != want {
			t.Errorf("baseURLFromEndpoint(%q)=%q want %q", in, got, want)
		}
	}
}
