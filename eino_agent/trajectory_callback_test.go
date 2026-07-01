package eino_agent

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

func TestTrajectoryHandler_ChatModelLeg(t *testing.T) {
	rec := &fakeRecorder{}
	base := withTrajectory(context.Background(), rec, "MiniMax-M2.7")
	b, _ := base.Value(trajCtxKey{}).(*trajBundle)
	if b == nil {
		t.Fatal("trajBundle not in ctx")
	}
	// 模拟 claude.Generate 的 ChatModel OnStart/OnEnd。
	ctx := callbacks.InitCallbacks(base, &callbacks.RunInfo{Component: components.ComponentOfChatModel}, b.handler)
	ctx = callbacks.OnStart(ctx, &model.CallbackInput{Messages: []*schema.Message{schema.UserMessage("hi")}})
	callbacks.OnEnd(ctx, &model.CallbackOutput{
		Message:    &schema.Message{Role: schema.Assistant, Content: "ans"},
		TokenUsage: &model.TokenUsage{PromptTokens: 7, CompletionTokens: 3},
	})
	if rec.llmCalls != 1 {
		t.Fatalf("want 1 RecordLLMCall via callback, got %d", rec.llmCalls)
	}
}
