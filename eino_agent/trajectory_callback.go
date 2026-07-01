package eino_agent

import (
	"context"
	"time"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"

	"github.com/RuntianLee/schema-driven-insight-agent/agent"
	"github.com/RuntianLee/schema-driven-insight-agent/llm"
)

type trajCtxKey struct{}
type startCtxKey struct{}
type promptCtxKey struct{}

type trajBundle struct {
	store     agent.TrajectoryStore
	modelName string
	handler   callbacks.Handler
}

// withTrajectory 把 per-Run 的 trajectory handler 注入 ctx（含 traj 句柄闭包），
// 使后续 claude.Generate（自动）与 recordedDispatch（显式）的 OnStart/OnEnd 命中、录 Record*。
func withTrajectory(ctx context.Context, store agent.TrajectoryStore, modelName string) context.Context {
	b := &trajBundle{store: store, modelName: modelName}
	b.handler = buildTrajHandler(b)
	ctx = context.WithValue(ctx, trajCtxKey{}, b)
	return callbacks.InitCallbacks(ctx, nil, b.handler)
}

func buildTrajHandler(b *trajBundle) callbacks.Handler {
	return callbacks.NewHandlerBuilder().
		OnStartFn(func(ctx context.Context, info *callbacks.RunInfo, in callbacks.CallbackInput) context.Context {
			ctx = context.WithValue(ctx, startCtxKey{}, time.Now())
			if info.Component == components.ComponentOfChatModel {
				if mi := model.ConvCallbackInput(in); mi != nil {
					ctx = context.WithValue(ctx, promptCtxKey{}, serializeMessages(mi.Messages))
				}
			}
			return ctx
		}).
		OnEndFn(func(ctx context.Context, info *callbacks.RunInfo, out callbacks.CallbackOutput) context.Context {
			start, _ := ctx.Value(startCtxKey{}).(time.Time)
			end := time.Now()
			switch info.Component {
			case components.ComponentOfChatModel:
				mo := model.ConvCallbackOutput(out)
				if mo == nil {
					return ctx
				}
				var tokIn, tokOut int
				if mo.TokenUsage != nil {
					tokIn, tokOut = mo.TokenUsage.PromptTokens, mo.TokenUsage.CompletionTokens
				}
				prompt, _ := ctx.Value(promptCtxKey{}).(string)
				resp := ""
				if mo.Message != nil {
					resp = serializeAssistant(mo.Message)
					if mo.Message.ReasoningContent != "" {
						b.store.RecordReasoning(mo.Message.ReasoningContent, start, end)
					}
				}
				b.store.RecordLLMCall(prompt, resp, b.modelName, tokIn, tokOut, llm.CostUSD(tokIn, tokOut), start, end, nil)
			case components.ComponentOfTool:
				to := tool.ConvCallbackOutput(out)
				name, _ := ctx.Value(toolNameCtxKey{}).(string)
				var output any
				if to != nil {
					output = to.Response
				}
				b.store.RecordToolCall(name, ctx.Value(toolArgsCtxKey{}), output, start, end, nil)
			}
			return ctx
		}).
		OnErrorFn(func(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
			start, _ := ctx.Value(startCtxKey{}).(time.Time)
			if info.Component == components.ComponentOfChatModel {
				b.store.RecordLLMCall("", "", b.modelName, 0, 0, 0, start, time.Now(), err)
			}
			return ctx
		}).
		Build()
}
