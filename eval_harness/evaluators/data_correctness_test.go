// framework/eval_harness/evaluators/data_correctness_test.go
package evaluators

import (
	"context"
	"strings"
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

func TestDataCorrectness_TableMatcher(t *testing.T) {
	res := TaskResult{ToolCalls: []ToolCall{{
		Name: "analyze",
		Response: contract.Response{
			Status: contract.StatusOK,
			Table: &contract.TableResult{
				Columns:  []contract.ColumnMeta{{Name: "server_id"}, {Name: "players"}, {Name: "total"}},
				Rows:     [][]any{{int64(1), int64(150), int64(90000000)}, {int64(2), int64(60), int64(42000000)}},
				RowCount: 2,
			},
		},
	}}}
	specYAML := `
tool: analyze
expect_status: OK
table:
  - match: {server_id: "1"}
    expect: {players: 150, total: 90000000}
  - match: {server_id: "2"}
    expect: {players: 60}
`
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(specYAML), &node); err != nil {
		t.Fatal(err)
	}
	score, err := NewDataCorrectness().Evaluate(context.Background(), res, node.Content[0])
	if err != nil {
		t.Fatal(err)
	}
	if !score.Pass {
		t.Fatalf("want pass, got %+v", score)
	}
}

func TestDataCorrectness_TableMatcherFails(t *testing.T) {
	res := TaskResult{ToolCalls: []ToolCall{{
		Name: "analyze",
		Response: contract.Response{Status: contract.StatusOK, Table: &contract.TableResult{
			Columns: []contract.ColumnMeta{{Name: "server_id"}, {Name: "players"}},
			Rows:    [][]any{{int64(1), int64(150)}},
		}},
	}}}
	specYAML := "tool: analyze\nexpect_status: OK\ntable:\n  - match: {server_id: \"1\"}\n    expect: {players: 999}\n"
	var node yaml.Node
	_ = yaml.Unmarshal([]byte(specYAML), &node)
	score, _ := NewDataCorrectness().Evaluate(context.Background(), res, node.Content[0])
	if score.Pass {
		t.Fatal("want fail on wrong expected value")
	}
}

func TestDataCorrectness_TableMatcherRejectsEmptyMatch(t *testing.T) {
	res := TaskResult{ToolCalls: []ToolCall{{
		Name: "analyze",
		Response: contract.Response{Status: contract.StatusOK, Table: &contract.TableResult{
			Columns: []contract.ColumnMeta{{Name: "server_id"}, {Name: "players"}},
			Rows:    [][]any{{int64(1), int64(150)}},
		}},
	}}}
	// 空 match 会误配首行 → 必须当作断言错误而非通过。
	specYAML := "tool: analyze\nexpect_status: OK\ntable:\n  - expect: {players: 150}\n"
	var node yaml.Node
	_ = yaml.Unmarshal([]byte(specYAML), &node)
	score, _ := NewDataCorrectness().Evaluate(context.Background(), res, node.Content[0])
	if score.Pass {
		t.Fatal("空 match 应判失败（防误配首行）")
	}
}

// TestDataCorrectness_TableExpectPos 验证按列位置（expect_pos）断言的正确性。
// 背景：真实 LLM 道中 agent 自由选取 as 别名，列名不可预测；
// expect_pos 按绝对列索引断言，使别名鲁棒的正确答案不被"未知列"误判为失败。
func TestDataCorrectness_TableExpectPos(t *testing.T) {
	// 列名刻意设为 agent 自选别名"去重等级数"，而非黄金标准所期待的名字
	tr := &contract.TableResult{
		Columns:  []contract.ColumnMeta{{Name: "server_id"}, {Name: "去重等级数"}},
		Rows:     [][]any{{"1", int64(2)}, {"2", int64(3)}},
		RowCount: 2,
	}

	// 1. 正确的位置断言 → 应通过（空 fails）
	t.Run("正确位置断言通过", func(t *testing.T) {
		fails := checkTable(tr, dcTableRow{
			Match:     map[string]string{"server_id": "1"},
			ExpectPos: map[int]float64{1: 2},
		})
		if len(fails) != 0 {
			t.Fatalf("expect pass, got fails: %v", fails)
		}
	})

	// 2. 错误的期望值 → 应失败
	t.Run("错误期望值失败", func(t *testing.T) {
		fails := checkTable(tr, dcTableRow{
			Match:     map[string]string{"server_id": "1"},
			ExpectPos: map[int]float64{1: 99},
		})
		if len(fails) == 0 {
			t.Fatal("expect fail on wrong value, got empty fails")
		}
	})

	// 3. 旧的按列名断言：列名不存在时应返回"未知列"错误（证明旧路径仍按原样拒绝未知别名）
	t.Run("按名断言未知列返回错误", func(t *testing.T) {
		fails := checkTable(tr, dcTableRow{
			Match:  map[string]string{"server_id": "1"},
			Expect: map[string]float64{"distinct_levels": 2},
		})
		if len(fails) == 0 {
			t.Fatal("expect fail for unknown column name, got empty fails")
		}
		found := false
		for _, f := range fails {
			if strings.Contains(f, "未知列") {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expect '未知列' in failures, got: %v", fails)
		}
	})

	// 4. 列索引越界 → 应失败
	t.Run("列索引越界失败", func(t *testing.T) {
		fails := checkTable(tr, dcTableRow{
			Match:     map[string]string{"server_id": "1"},
			ExpectPos: map[int]float64{5: 2},
		})
		if len(fails) == 0 {
			t.Fatal("expect fail for out-of-range column index, got empty fails")
		}
	})
}
