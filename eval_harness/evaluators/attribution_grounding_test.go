// framework/eval_harness/evaluators/attribution_grounding_test.go
package evaluators

import (
	"context"
	"strings"
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

func attrResult(claims []contract.ClaimAnchor) TaskResult {
	return TaskResult{ToolCalls: attrToolCalls(), AttributionClaims: claims}
}

func TestAttributionGrounding_MinClaims(t *testing.T) {
	resolved := []contract.ClaimAnchor{{Claim: "均值0.6", Anchor: "q1.profile.mean", Kind: "cell", ClaimedValue: 0.6}}
	mismatch := []contract.ClaimAnchor{{Claim: "均值9999", Anchor: "q1.profile.mean", Kind: "cell", ClaimedValue: 9999}}
	ctx := context.Background()

	// 1) 未设 min_claims（spec=nil）+ nil → skip（向后兼容守卫）
	if s, err := NewAttributionGrounding().Evaluate(ctx, attrResult(nil), nil); err != nil || !s.Pass || !strings.Contains(s.Display, "skip") {
		t.Fatalf("未设 min_claims + nil 应 skip pass，得到 %+v err=%v", s, err)
	}
	// 2) min_claims:1 + nil → FAIL
	if s, _ := NewAttributionGrounding().Evaluate(ctx, attrResult(nil), specNode(t, "min_claims: 1")); s.Pass {
		t.Fatalf("min_claims:1 + 缺块应 FAIL，得到 %+v", s)
	}
	// 3) min_claims:1 + 1 resolved → pass
	if s, _ := NewAttributionGrounding().Evaluate(ctx, attrResult(resolved), specNode(t, "min_claims: 1")); !s.Pass {
		t.Fatalf("min_claims:1 + 1 resolved 应 pass，得到 %+v", s)
	}
	// 4) min_claims:2 + 1 条 → FAIL（不足），Display 须诚实显示实得分子 1/2
	if s, _ := NewAttributionGrounding().Evaluate(ctx, attrResult(resolved), specNode(t, "min_claims: 2")); s.Pass || !strings.Contains(s.Display, "1/2") {
		t.Fatalf("min_claims:2 + 仅1条应 FAIL 且 Display 含 1/2，得到 %+v", s)
	}
	// 5) min_claims:1 + 1 mismatch → FAIL（现有 bad 逻辑不变）
	if s, _ := NewAttributionGrounding().Evaluate(ctx, attrResult(mismatch), specNode(t, "min_claims: 1")); s.Pass {
		t.Fatalf("min_claims:1 + mismatch 应 FAIL，得到 %+v", s)
	}
	// 6) min_claims:0 显式 + nil → skip（与未设等价）
	if s, _ := NewAttributionGrounding().Evaluate(ctx, attrResult(nil), specNode(t, "min_claims: 0")); !s.Pass {
		t.Fatalf("min_claims:0 + nil 应 skip pass，得到 %+v", s)
	}
}
