// framework/eval_harness/evaluators/claim_coverage_test.go
package evaluators

import (
	"context"
	"strings"
	"testing"

	"github.com/RuntianLee/schema-driven-insight-agent/contract"
)

func TestClaimCoverage_NameAndDeterministic(t *testing.T) {
	e := NewClaimCoverage(NewMockJudge())
	if e.Name() != "claim_coverage" {
		t.Fatalf("want name=claim_coverage, got %q", e.Name())
	}
	if e.Deterministic() {
		t.Fatalf("ClaimCoverage 是 off-gate evaluator，Deterministic() 应为 false")
	}
}

func TestClaimCoverage(t *testing.T) {
	claims1 := []contract.ClaimAnchor{{Claim: "主张1", Anchor: "q1.a", Kind: "cell", ClaimedValue: 1}}
	extracted4 := `["主张A","主张B","主张C","主张D"]`
	extracted1 := `["主张A"]`

	tests := []struct {
		name        string
		client      string // constJudge resp
		claims      []contract.ClaimAnchor
		answer      string
		wantDisplay string   // Display 包含此子串
		wantValue   *float64 // nil = 不校验 Value
		wantPass    bool
		wantBelowMin bool
	}{
		{
			name:        "空 claims → skip",
			client:      extracted1,
			claims:      nil,
			answer:      "有数字",
			wantDisplay: "skip",
		},
		{
			name:        "空 answer → skip",
			client:      extracted1,
			claims:      claims1,
			answer:      "",
			wantDisplay: "skip",
		},
		{
			name:        "mock 提取 1 条，声明 1 条 → rate=1.0",
			client:      extracted1,
			claims:      claims1,
			answer:      "ARPU 为 12.5",
			wantValue:   ptr(1.0),
			wantPass:    false,
			wantBelowMin: false,
		},
		{
			name:    "mock 提取 4 条，声明 1 条 → rate=0.25",
			client:  extracted4,
			claims:  claims1,
			answer:  "有四条定量主张",
			wantValue: ptr(0.25),
			wantPass:    false,
			wantBelowMin: false,
		},
		{
			name:        "LLM 返回非 JSON → BelowMin=true，不 panic",
			client:      "这不是 JSON",
			claims:      claims1,
			answer:      "有主张",
			wantBelowMin: true,
			wantPass:    false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			e := NewClaimCoverage(constJudge(tc.client))
			s, err := e.Evaluate(context.Background(), TaskResult{
				Answer:            tc.answer,
				AttributionClaims: tc.claims,
			}, nil)

			if err != nil {
				t.Fatalf("Evaluate 不应返回 error, got: %v", err)
			}
			if s.Pass {
				t.Fatalf("Pass 应永远为 false（off-gate），got true")
			}
			if tc.wantDisplay != "" && !strings.Contains(s.Display, tc.wantDisplay) {
				t.Fatalf("Display 应含 %q，got %q", tc.wantDisplay, s.Display)
			}
			if tc.wantValue != nil && absF(s.Value-*tc.wantValue) > 0.01 {
				t.Fatalf("want Value≈%.2f, got %.4f", *tc.wantValue, s.Value)
			}
			if s.BelowMin != tc.wantBelowMin {
				t.Fatalf("BelowMin want %v got %v", tc.wantBelowMin, s.BelowMin)
			}
		})
	}
}

func ptr(f float64) *float64 { return &f }

func absF(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
