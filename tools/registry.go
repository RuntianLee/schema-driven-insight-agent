// Package tools 是 schema-driven 的 tool 集合。V0 仅 query_distribution。
package tools

import (
	"context"
	"fmt"

	"github.com/RuntianLee/schema-driven-insight-agent/contract"
)

type Handler func(ctx context.Context, args map[string]any) (contract.Response, error)

// Registry 实现 agent.ToolDispatcher（结构化类型桥接）。
type Registry struct {
	handlers map[string]Handler
}

func NewRegistry() *Registry {
	return &Registry{handlers: make(map[string]Handler)}
}

func (r *Registry) Register(name string, h Handler) {
	r.handlers[name] = h
}

func (r *Registry) Dispatch(ctx context.Context, name string, args map[string]any) (contract.Response, error) {
	h, ok := r.handlers[name]
	if !ok {
		return contract.Response{
			Status: contract.StatusSchemaError,
			Hint:   fmt.Sprintf("unknown tool %q", name),
		}, nil
	}
	return h(ctx, args)
}
