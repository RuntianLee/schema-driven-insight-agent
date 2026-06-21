// cmd/agent/advisor_pipeline_test.go
package main

import (
	"context"
	"strings"
	"testing"

	"github.com/RuntianLee/schema-driven-insight-agent/contract"
	"github.com/RuntianLee/schema-driven-insight-agent/tools"
)

// seqLLM 按序返回脚本响应（Analyst 工具轮 → Analyst 答案 → Advisor 草案）。
type seqLLM struct {
	resps []string
	i     int
}

func (s *seqLLM) Call(_ context.Context, _ string) (string, int, int, float64, error) {
	r := s.resps[s.i]
	if s.i < len(s.resps)-1 {
		s.i++
	}
	return r, 0, 0, 0, nil
}
func (s *seqLLM) Model() string { return "seq" }

func TestRunAdvisePipeline(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register("analyze", func(_ context.Context, _ map[string]any) (contract.Response, error) {
		return contract.Response{Status: contract.StatusOK}, nil
	})
	client := &seqLLM{resps: []string{
		`{"tool":"analyze","args":{"table":"customers"}}`,
		"德国流失显著偏高。",
		`{"summary":"建议草案","items":[{"observation":"德国流失高","source_ref":"q1","action":"排查德国体验","priority":"high","caveat":"草案"}]}`,
	}}

	answer, draft, err := runAdvisePipeline(context.Background(), client, reg, "SCHEMA-DIGEST", "playbook 文本", "各国客户流失？")
	if err != nil {
		t.Fatalf("pipeline: %v", err)
	}
	if !strings.Contains(answer, "德国") {
		t.Errorf("analyst answer=%q", answer)
	}
	if len(draft.Items) != 1 || draft.Items[0].SourceRef != "q1" {
		t.Fatalf("draft=%+v", draft)
	}

	out := renderAdvisory(draft)
	if !strings.Contains(out, "排查德国体验") || !strings.Contains(out, "建议草案") {
		t.Errorf("render=%q", out)
	}
}
