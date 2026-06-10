// framework/eval_harness/evaluators/evaluator_test.go
package evaluators

import (
	"context"
	"testing"

	"gopkg.in/yaml.v3"
)

type stubEval struct{ name string; det bool }

func (s stubEval) Name() string          { return s.name }
func (s stubEval) Deterministic() bool    { return s.det }
func (s stubEval) Evaluate(_ context.Context, _ TaskResult, _ *yaml.Node) (Score, error) {
	return Score{Evaluator: s.name, Value: 1.0, Pass: true}, nil
}

func TestRegistryRegisterAndGet(t *testing.T) {
	reg := NewRegistry()
	reg.Register(stubEval{name: "x", det: true})
	got, ok := reg.Get("x")
	if !ok {
		t.Fatalf("expected evaluator x registered")
	}
	if got.Name() != "x" || !got.Deterministic() {
		t.Fatalf("unexpected evaluator: %+v", got)
	}
	if _, ok := reg.Get("missing"); ok {
		t.Fatalf("missing evaluator should not be found")
	}
}
