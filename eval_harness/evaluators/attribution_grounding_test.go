// framework/eval_harness/evaluators/attribution_grounding_test.go
package evaluators

import (
	"context"
	"testing"

	"github.com/RuntianLee/schema-driven-insight-agent/contract"
)

// attrToolCalls 构造包含单次 analyze 结果的 ToolCall 切片，profile.mean=0.6，count=100。
func attrToolCalls() []contract.ToolCall {
	return []contract.ToolCall{{
		Name: "analyze",
		Args: map[string]any{},
		Response: contract.Response{
			Status:  contract.StatusOK,
			Profile: &contract.DistProfile{Count: 100, Mean: 0.6},
		},
	}}
}

func TestAttributionGrounding_NameAndDeterministic(t *testing.T) {
	e := NewAttributionGrounding()
	if e.Name() != "attribution_grounding" {
		t.Fatalf("name: got %q", e.Name())
	}
	if !e.Deterministic() {
		t.Fatal("should be deterministic")
	}
}

func TestAttributionGrounding_Table(t *testing.T) {
	calls := attrToolCalls()
	tests := []struct {
		name      string
		claims    []contract.ClaimAnchor
		wantPass  bool
		wantValue float64 // 仅 assertVal=true 时校验
		assertVal bool
	}{
		{
			name:     "nil claims → skip, pass=true",
			claims:   nil,
			wantPass: true,
		},
		{
			name:      "all resolved → pass",
			claims:    []contract.ClaimAnchor{{Claim: "mean 0.6", Anchor: "q1.profile.mean", Kind: "cell", ClaimedValue: 0.6}},
			wantPass:  true,
			wantValue: 1.0,
			assertVal: true,
		},
		{
			name:      "mismatch → fail",
			claims:    []contract.ClaimAnchor{{Claim: "mean 0.9", Anchor: "q1.profile.mean", Kind: "cell", ClaimedValue: 0.9}},
			wantPass:  false,
			wantValue: 0.0,
			assertVal: true,
		},
		{
			name:     "unresolvable anchor → fail",
			claims:   []contract.ClaimAnchor{{Claim: "bogus", Anchor: "q1.profile.nonexistent_field", Kind: "cell", ClaimedValue: 1.0}},
			wantPass: false,
		},
		{
			name:     "derived_unsupported → pass (宽容, 不计 bad)",
			claims:   []contract.ClaimAnchor{{Claim: "比", Anchor: "unknown_op(q1.profile.mean,q1.profile.count)", Kind: "derived", ClaimedValue: 0.006}},
			wantPass: true,
		},
		{
			name: "mixed resolved+mismatch+derived_unsupported → fail",
			claims: []contract.ClaimAnchor{
				{Claim: "mean 0.6", Anchor: "q1.profile.mean", Kind: "cell", ClaimedValue: 0.6},
				{Claim: "mean 0.9", Anchor: "q1.profile.mean", Kind: "cell", ClaimedValue: 0.9},
				{Claim: "比", Anchor: "unknown_op(q1.profile.mean,q1.profile.count)", Kind: "derived", ClaimedValue: 0.1},
			},
			wantPass:  false,
			wantValue: 2.0 / 3.0, // 仅 mismatch 计 bad；derived_unsupported 宽容 → 2 good / 3 ≈ 0.667
			assertVal: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := TaskResult{ToolCalls: calls, AttributionClaims: tt.claims}
			s, err := NewAttributionGrounding().Evaluate(context.Background(), res, nil)
			if err != nil {
				t.Fatal(err)
			}
			if s.Pass != tt.wantPass {
				t.Errorf("Pass=%v (want %v), Display=%q", s.Pass, tt.wantPass, s.Display)
			}
			if tt.assertVal && (s.Value < tt.wantValue-0.01 || s.Value > tt.wantValue+0.01) {
				t.Errorf("Value=%v (want ~%v)", s.Value, tt.wantValue)
			}
			if s.Evaluator != "attribution_grounding" {
				t.Errorf("Evaluator name: %q", s.Evaluator)
			}
		})
	}
}
