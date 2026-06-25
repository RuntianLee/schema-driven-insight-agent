// framework/eval_harness/evaluators/evaluator_test.go
package evaluators

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/RuntianLee/schema-driven-insight-agent/contract"
	"gopkg.in/yaml.v3"
)

type stubEval struct {
	name string
	det  bool
}

func (s stubEval) Name() string        { return s.name }
func (s stubEval) Deterministic() bool { return s.det }
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

func TestClaimAnchorJSON(t *testing.T) {
	orig := []contract.ClaimAnchor{
		{Claim: "流失率 60%", Anchor: "q1.profile.mean", Kind: "cell", ClaimedValue: 0.6},
		{Claim: "人均 200k", Anchor: "ratio(q1.profile.mean,q1.profile.count)", Kind: "derived", ClaimedValue: 200000},
	}
	b, err := json.Marshal(orig)
	if err != nil {
		t.Fatal(err)
	}
	var got []contract.ClaimAnchor
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Claim != "流失率 60%" || got[1].Kind != "derived" {
		t.Fatalf("往返失真: %+v", got)
	}
	if got[0].ClaimedValue != 0.6 || got[1].ClaimedValue != 200000 {
		t.Fatalf("ClaimedValue 往返失真: %+v", got)
	}
}
