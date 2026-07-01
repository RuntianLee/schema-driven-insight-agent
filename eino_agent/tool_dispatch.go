package eino_agent

import (
	"context"
	"encoding/json"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/tool"

	"github.com/RuntianLee/schema-driven-insight-agent/agent"
	"github.com/RuntianLee/schema-driven-insight-agent/contract"
)

type toolNameCtxKey struct{}
type toolArgsCtxKey struct{}
type toolRespCtxKey struct{}

// recordedDispatch 调 ToolDispatcher.Dispatch，并发 Tool 组件回调（OnStart/OnEnd）使
// per-Run handler 录 RecordToolCall——业务循环不再手工 Record*（zero-invasion）。
func recordedDispatch(ctx context.Context, d agent.ToolDispatcher, name string, args map[string]any) (contract.Response, error) {
	// 把 name/args 透传给 handler（tool 回调载荷不带结构化 name/args）。
	ctx = context.WithValue(ctx, toolNameCtxKey{}, name)
	ctx = context.WithValue(ctx, toolArgsCtxKey{}, args)
	ctx = callbacks.EnsureRunInfo(ctx, "ToolDispatcher", components.ComponentOfTool)
	argsJSON, _ := json.Marshal(args)
	ctx = callbacks.OnStart(ctx, &tool.CallbackInput{ArgumentsInJSON: string(argsJSON)})
	resp, err := d.Dispatch(ctx, name, args)
	if err != nil {
		callbacks.OnError(ctx, err)
		return resp, err
	}
	respJSON, _ := json.Marshal(resp)
	ctx = context.WithValue(ctx, toolRespCtxKey{}, resp) // 透传结构化 contract.Response 给 handler（保 trajcapture 类型断言）
	callbacks.OnEnd(ctx, &tool.CallbackOutput{Response: string(respJSON)})
	return resp, nil
}
