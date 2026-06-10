package agent

import (
	"context"

	"github.com/RuntianLee/schema-driven-insight-agent/contract"
)

// ToolDispatcher 解析并执行 tool，独立于 LLM。
type ToolDispatcher interface {
	Dispatch(ctx context.Context, name string, args map[string]any) (contract.Response, error)
}
