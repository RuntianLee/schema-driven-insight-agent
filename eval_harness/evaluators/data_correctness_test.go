// framework/eval_harness/evaluators/data_correctness_test.go
package evaluators

import (
	"context"
	"testing"

	"github.com/RuntianLee/schema-driven-insight-agent/contract"
	"gopkg.in/yaml.v3"
)

func specNode(t *testing.T, y string) *yaml.Node {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(y), &n); err != nil {
		t.Fatalf("yaml: %v", err)
	}
	return n.Content[0] // 解包 document node
}

func resultWith(resp contract.Response) TaskResult {
	return TaskResult{TaskID: "t", ToolCalls: []ToolCall{{Name: "query_distribution", Response: resp}}}
}

func TestDataCorrectness_RowsAndProfilePass(t *testing.T) {
	resp := contract.Response{
		Status:  contract.StatusOK,
		Profile: &contract.DistProfile{Count: 250},
		Data:    []contract.BucketRow{{Bucket: "20", PlayerCount: 150}},
	}
	spec := specNode(t, `
tool: query_distribution
expect_status: OK
profile: {count: 250}
rows:
  - match: {bucket: "20"}
    expect: {player_count: 150}
`)
	s, err := NewDataCorrectness().Evaluate(context.Background(), resultWith(resp), spec)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if !s.Pass || s.Value != 1.0 {
		t.Fatalf("want pass 1.0, got %+v", s)
	}
}

func TestDataCorrectness_RowMismatchFails(t *testing.T) {
	resp := contract.Response{Status: contract.StatusOK,
		Data: []contract.BucketRow{{Bucket: "20", PlayerCount: 999}}}
	spec := specNode(t, `
tool: query_distribution
expect_status: OK
rows:
  - match: {bucket: "20"}
    expect: {player_count: 150}
`)
	s, _ := NewDataCorrectness().Evaluate(context.Background(), resultWith(resp), spec)
	if s.Pass || s.Value != 0.0 {
		t.Fatalf("want fail 0.0, got %+v", s)
	}
	if s.Detail == "" {
		t.Fatalf("fail must carry detail")
	}
}

func TestDataCorrectness_GroupsPass(t *testing.T) {
	resp := contract.Response{
		Status: contract.StatusOK,
		Groups: []contract.GroupProfile{
			{Group: "1", Data: []contract.BucketRow{{Bucket: "20", PlayerCount: 80}}},
			{Group: "2", Data: []contract.BucketRow{{Bucket: "20", PlayerCount: 120}}},
		},
	}
	spec := specNode(t, `
tool: query_distribution
expect_status: OK
groups:
  - match: {group: "2"}
    rows:
      - match: {bucket: "20"}
        expect: {player_count: 120}
`)
	s, err := NewDataCorrectness().Evaluate(context.Background(), resultWith(resp), spec)
	if err != nil || !s.Pass {
		t.Fatalf("want pass, got %+v err=%v", s, err)
	}
}

func TestDataCorrectness_DeterministicAndName(t *testing.T) {
	e := NewDataCorrectness()
	if e.Name() != "data_correctness" || !e.Deterministic() {
		t.Fatalf("unexpected: %s det=%v", e.Name(), e.Deterministic())
	}
}

func TestDataCorrectness_MissingToolFails(t *testing.T) {
	spec := specNode(t, "tool: nonexistent\nexpect_status: OK\n")
	s, _ := NewDataCorrectness().Evaluate(context.Background(), resultWith(contract.Response{Status: contract.StatusOK}), spec)
	if s.Pass {
		t.Fatalf("missing tool call should fail")
	}
}
