package evaluators

import (
	"context"
	"testing"

	"github.com/RuntianLee/schema-driven-insight-agent/contract"
)

// agent 允许 SCHEMA_ERROR 后自修正重试：评分必须取最后一次成功调用，
// 不得按第一次失败调用打 0 分（真 LLM 评测道的实际轨迹形态）。
func TestDataCorrectness_SelfCorrectionTakesLastOK(t *testing.T) {
	bad := contract.Response{Status: contract.StatusSchemaError}
	good := contract.Response{
		Status: contract.StatusOK,
		Data:   []contract.BucketRow{{Bucket: "20", PlayerCount: 150}},
	}
	res := TaskResult{TaskID: "t", ToolCalls: []ToolCall{
		{Name: "query_distribution", Response: bad},
		{Name: "query_distribution", Response: good},
	}}
	spec := specNode(t, `
tool: query_distribution
expect_status: OK
rows:
  - match: {bucket: "20"}
    expect: {player_count: 150}
`)
	s, err := NewDataCorrectness().Evaluate(context.Background(), res, spec)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if !s.Pass || s.Value != 1.0 {
		t.Fatalf("自修正轨迹应按最后一次成功调用通过, got %+v", s)
	}
}
