package tools

import (
	"context"
	"testing"

	"github.com/RuntianLee/schema-driven-insight-agent/contract"
)

func TestRegistryDispatch(t *testing.T) {
	r := NewRegistry()
	r.Register("echo", func(_ context.Context, args map[string]any) (contract.Response, error) {
		return contract.Response{Status: contract.StatusOK, Detail: args}, nil
	})

	resp, err := r.Dispatch(context.Background(), "echo", map[string]any{"k": "v"})
	if err != nil || resp.Status != contract.StatusOK {
		t.Fatalf("dispatch echo: %v %+v", err, resp)
	}

	resp, err = r.Dispatch(context.Background(), "missing", nil)
	if err != nil {
		t.Fatalf("unknown tool must return nil error, got %v", err)
	}
	if resp.Status != contract.StatusSchemaError {
		t.Fatalf("unknown tool must yield SCHEMA_ERROR, got %s", resp.Status)
	}
}
