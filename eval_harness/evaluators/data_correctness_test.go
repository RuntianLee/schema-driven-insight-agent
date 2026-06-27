// framework/eval_harness/evaluators/data_correctness_test.go
package evaluators

import (
	"context"
	"fmt"
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
	return TaskResult{TaskID: "t", ToolCalls: []contract.ToolCall{{Name: "query_distribution", Response: resp}}}
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
	res := TaskResult{ToolCalls: []contract.ToolCall{{
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
	res := TaskResult{ToolCalls: []contract.ToolCall{{
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
	res := TaskResult{ToolCalls: []contract.ToolCall{{
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

func TestDataCorrectness_TableExpectAnyAllowsCoreMetricColumnReorder(t *testing.T) {
	tr := &contract.TableResult{
		Columns: []contract.ColumnMeta{
			{Name: "server_id"},
			{Name: "player_count"},
			{Name: "avg_virtual_money"},
		},
		Rows: [][]any{{int64(1), int64(100), int64(2000)}},
	}
	fails := checkTable(tr, dcTableRow{
		Match: map[string]string{"server_id": "1"},
		ExpectAny: []dcTableExpectAny{{
			Columns: []string{"avg_virtual_money", "avg_money"},
			Value:   2000,
		}},
	})
	if len(fails) != 0 {
		t.Fatalf("expect_any should pass despite count column before core metric, got fails: %v", fails)
	}
}

func TestDataCorrectness_TableSpecPassesWhenAnySuccessfulToolResponseMatches(t *testing.T) {
	res := TaskResult{ToolCalls: []contract.ToolCall{
		{
			Name: "analyze",
			Response: contract.Response{
				Status: contract.StatusOK,
				Table: &contract.TableResult{
					Columns:  []contract.ColumnMeta{{Name: "server_id"}, {Name: "never_online_count"}},
					Rows:     [][]any{{int64(1), int64(20)}, {int64(2), int64(60)}},
					RowCount: 2,
				},
			},
		},
		{
			Name: "analyze",
			Response: contract.Response{
				Status: contract.StatusOK,
				Table: &contract.TableResult{
					Columns:  []contract.ColumnMeta{{Name: "server_id"}, {Name: "total_players"}},
					Rows:     [][]any{{int64(1), int64(100)}, {int64(2), int64(100)}},
					RowCount: 2,
				},
			},
		},
	}}
	spec := specNode(t, `
tool: analyze
expect_status: OK
table:
  - match: {server_id: "1"}
    expect_any:
      - columns: ["never_online_count", "n"]
        value: 20
  - match: {server_id: "2"}
    expect_any:
      - columns: ["never_online_count", "n"]
        value: 60
`)
	score, err := NewDataCorrectness().Evaluate(context.Background(), res, spec)
	if err != nil {
		t.Fatal(err)
	}
	if !score.Pass {
		t.Fatalf("expected any matching analyze response to satisfy spec, got %+v", score)
	}
}

func TestDataCorrectness_SingleRowAddressesSoleRow(t *testing.T) {
	tr := &contract.TableResult{
		Columns: []contract.ColumnMeta{{Name: "total"}, {Name: "churned"}},
		Rows:    [][]any{{int64(30), int64(18)}},
	}
	t.Run("聚合单行按列名通过", func(t *testing.T) {
		fails := checkTable(tr, dcTableRow{
			SingleRow: true,
			ExpectAny: []dcTableExpectAny{
				{Columns: []string{"total", "count", "n"}, Value: 30},
				{Columns: []string{"churned", "sum_Exited"}, Value: 18},
			},
		})
		if len(fails) != 0 {
			t.Fatalf("expect pass, got fails: %v", fails)
		}
	})
	t.Run("错值失败", func(t *testing.T) {
		fails := checkTable(tr, dcTableRow{
			SingleRow: true,
			ExpectAny: []dcTableExpectAny{{Columns: []string{"churned"}, Value: 999}},
		})
		if len(fails) == 0 {
			t.Fatal("expect fail on wrong value")
		}
	})
	t.Run("多行时失败报行数", func(t *testing.T) {
		multi := &contract.TableResult{
			Columns: []contract.ColumnMeta{{Name: "Exited"}, {Name: "n"}},
			Rows:    [][]any{{int64(0), int64(12)}, {int64(1), int64(18)}},
		}
		fails := checkTable(multi, dcTableRow{
			SingleRow: true,
			ExpectAny: []dcTableExpectAny{{Columns: []string{"n"}, Value: 12}},
		})
		if len(fails) == 0 || !strings.Contains(strings.Join(fails, ";"), "2 行") {
			t.Fatalf("expect fail mentioning 实际行数, got: %v", fails)
		}
	})
}

func TestDataCorrectness_AnyOfPassesEitherShape(t *testing.T) {
	groupByResp := contract.Response{Status: contract.StatusOK, Table: &contract.TableResult{
		Columns: []contract.ColumnMeta{{Name: "Exited"}, {Name: "n"}},
		Rows:    [][]any{{int64(0), int64(12)}, {int64(1), int64(18)}},
	}}
	aggResp := contract.Response{Status: contract.StatusOK, Table: &contract.TableResult{
		Columns: []contract.ColumnMeta{{Name: "total"}, {Name: "churned"}},
		Rows:    [][]any{{int64(30), int64(18)}},
	}}
	spec := specNode(t, `
tool: analyze
expect_status: OK
any_of:
  - table:
    - match: {Exited: "0"}
      expect_any: [{columns: ["n","count"], value: 12}]
    - match: {Exited: "1"}
      expect_any: [{columns: ["n","count"], value: 18}]
  - table:
    - single_row: true
      expect_any:
        - {columns: ["total","count","n"], value: 30}
        - {columns: ["churned","sum_Exited"], value: 18}
`)
	for name, resp := range map[string]contract.Response{"group_by": groupByResp, "aggregate": aggResp} {
		res := TaskResult{ToolCalls: []contract.ToolCall{{Name: "analyze", Response: resp}}}
		s, err := NewDataCorrectness().Evaluate(context.Background(), res, spec)
		if err != nil {
			t.Fatalf("%s: evaluate err %v", name, err)
		}
		if !s.Pass {
			t.Fatalf("%s shape should pass via any_of, got %+v", name, s)
		}
	}
}

func TestDataCorrectness_AnyOfFailsWhenAllBranchesWrong(t *testing.T) {
	resp := contract.Response{Status: contract.StatusOK, Table: &contract.TableResult{
		Columns: []contract.ColumnMeta{{Name: "total"}, {Name: "churned"}},
		Rows:    [][]any{{int64(99), int64(99)}},
	}}
	spec := specNode(t, `
tool: analyze
expect_status: OK
any_of:
  - table:
    - match: {Exited: "1"}
      expect_any: [{columns: ["n"], value: 18}]
  - table:
    - single_row: true
      expect_any: [{columns: ["churned"], value: 18}]
`)
	res := TaskResult{ToolCalls: []contract.ToolCall{{Name: "analyze", Response: resp}}}
	s, _ := NewDataCorrectness().Evaluate(context.Background(), res, spec)
	if s.Pass {
		t.Fatal("all branches wrong → must fail")
	}
	if !strings.Contains(s.Detail, "any_of 全分支未过") || !strings.Contains(s.Detail, "分支1") || !strings.Contains(s.Detail, "分支2") {
		t.Fatalf("detail must render all branch fails, got: %q", s.Detail)
	}
}

func TestDataCorrectness_RejectsConflictingSpec(t *testing.T) {
	res := TaskResult{ToolCalls: []contract.ToolCall{{Name: "analyze",
		Response: contract.Response{Status: contract.StatusOK, Table: &contract.TableResult{
			Columns: []contract.ColumnMeta{{Name: "n"}}, Rows: [][]any{{int64(1)}}}}}}}

	t.Run("any_of 与顶层 table 互斥", func(t *testing.T) {
		spec := specNode(t, `
tool: analyze
table:
  - match: {Exited: "1"}
    expect_any: [{columns: ["n"], value: 1}]
any_of:
  - table:
    - single_row: true
      expect_any: [{columns: ["n"], value: 1}]
`)
		_, err := NewDataCorrectness().Evaluate(context.Background(), res, spec)
		if err == nil {
			t.Fatal("any_of + 顶层 table 应返回配置错误")
		}
	})

	t.Run("single_row 与 match 互斥", func(t *testing.T) {
		spec := specNode(t, `
tool: analyze
any_of:
  - table:
    - single_row: true
      match: {Exited: "1"}
      expect_any: [{columns: ["n"], value: 1}]
`)
		_, err := NewDataCorrectness().Evaluate(context.Background(), res, spec)
		if err == nil {
			t.Fatal("single_row + match 应返回配置错误")
		}
	})

	t.Run("空 any_of 分支被拒", func(t *testing.T) {
		spec := specNode(t, `
tool: analyze
any_of:
  - {}
`)
		_, err := NewDataCorrectness().Evaluate(context.Background(), res, spec)
		if err == nil {
			t.Fatal("空 any_of 分支（无任何断言）应返回配置错误")
		}
	})
}

func TestDataCorrectness_ExpectValuesNameBindingRegression(t *testing.T) {
	row := []any{int64(20), int64(12)}
	idx := map[string]int{"total": 0, "churned": 1}
	fails := checkExpectValues(row, idx, map[string]string{"row": "single"}, []dcValueBind{
		{Candidates: []string{"total", "n", "count"}, Value: 20},
		{Candidates: []string{"churned", "exited"}, Value: 12},
	})
	if len(fails) != 0 {
		t.Fatalf("已识别列名应强绑定通过，got: %v", fails)
	}
}

func TestDataCorrectness_ExpectValuesExoticAliasFallbackPasses(t *testing.T) {
	row := []any{int64(20), int64(12)}
	idx := map[string]int{"total_count": 0, "churned_count": 1} // 候选集外别名
	fails := checkExpectValues(row, idx, map[string]string{"row": "single"}, []dcValueBind{
		{Candidates: []string{"total", "n", "count"}, Value: 20},
		{Candidates: []string{"churned", "exited"}, Value: 12},
	})
	if len(fails) != 0 {
		t.Fatalf("候选列名全不存在应按值兜底通过，got: %v", fails)
	}
}

func TestDataCorrectness_ExpectValuesKnownColumnWrongValueFails(t *testing.T) {
	row := []any{int64(99), int64(12)} // total 列存在但值错
	idx := map[string]int{"total": 0, "churned": 1}
	fails := checkExpectValues(row, idx, map[string]string{"row": "single"}, []dcValueBind{
		{Candidates: []string{"total", "n"}, Value: 20},
	})
	if len(fails) == 0 {
		t.Fatal("已识别列名值错应 FAIL（不兜底放水）")
	}
}

func TestDataCorrectness_ExpectValuesExoticAliasWrongValueFails(t *testing.T) {
	row := []any{int64(99), int64(99)} // 生僻别名且值不在行
	idx := map[string]int{"total_count": 0, "churned_count": 1}
	fails := checkExpectValues(row, idx, map[string]string{"row": "single"}, []dcValueBind{
		{Candidates: []string{"total", "n"}, Value: 20},
	})
	if len(fails) == 0 {
		t.Fatal("生僻别名且值不在行应 FAIL")
	}
}

func TestDataCorrectness_ExpectValuesDistinctCellRequired(t *testing.T) {
	// 单 cell 值 12，两个兜底量都要 12：一个能认领、另一个应因无未占用 cell 而 FAIL。
	row := []any{int64(12)}
	idx := map[string]int{"x": 0}
	fails := checkExpectValues(row, idx, map[string]string{"row": "single"}, []dcValueBind{
		{Candidates: []string{"total"}, Value: 12},
		{Candidates: []string{"churned"}, Value: 12},
	})
	if len(fails) == 0 {
		t.Fatal("两个兜底量不得复用同一 cell，应 FAIL")
	}
}

func TestDataCorrectness_ExpectValuesTwoDistinctCellsPass(t *testing.T) {
	// 两个 cell 各为 12，两个兜底量各认领一个 → PASS。
	row := []any{int64(12), int64(12)}
	idx := map[string]int{"x": 0, "y": 1}
	fails := checkExpectValues(row, idx, map[string]string{"row": "single"}, []dcValueBind{
		{Candidates: []string{"total"}, Value: 12},
		{Candidates: []string{"churned"}, Value: 12},
	})
	if len(fails) != 0 {
		t.Fatalf("两个不同 cell 应各自认领通过，got: %v", fails)
	}
}

// 已接受残留 ④：两量都用生僻别名 + 完整对调（总数 12/流失 20），distinct-cell 各占一格 → PASS。
// 业务荒谬但由叙述维度兜住；此测锁定当前行为防回归误改。
func TestDataCorrectness_ExpectValuesAcceptedResidualFullSwap(t *testing.T) {
	row := []any{int64(12), int64(20)} // 生僻别名，值对调
	idx := map[string]int{"a_count": 0, "b_count": 1}
	fails := checkExpectValues(row, idx, map[string]string{"row": "single"}, []dcValueBind{
		{Candidates: []string{"total"}, Value: 20},
		{Candidates: []string{"churned"}, Value: 12},
	})
	if len(fails) != 0 {
		t.Fatalf("已接受残留：双生僻别名对调当前判 PASS，got: %v", fails)
	}
}

func TestDataCorrectness_ExpectValuesViaEvaluateAnyOf(t *testing.T) {
	// agent 用候选集外别名 total_count/churned_count（F-06 留痕场景）的 ungrouped 单行。
	resp := contract.Response{Status: contract.StatusOK, Table: &contract.TableResult{
		Columns: []contract.ColumnMeta{{Name: "total_count"}, {Name: "churned_count"}},
		Rows:    [][]any{{int64(20), int64(12)}},
	}}
	res := TaskResult{ToolCalls: []contract.ToolCall{{Name: "analyze", Response: resp}}}
	spec := specNode(t, `
tool: analyze
expect_status: OK
any_of:
  - table:
    - single_row: true
      expect_values:
        - {candidates: ["total", "count", "n", "cnt"], value: 20}
        - {candidates: ["churned", "sum_Exited", "exited"], value: 12}
`)
	score, err := NewDataCorrectness().Evaluate(context.Background(), res, spec)
	if err != nil {
		t.Fatal(err)
	}
	if !score.Pass {
		t.Fatalf("候选集外别名应按值兜底 PASS，got %+v", score)
	}
}

// 守护：接入后 expect_values 值错必须 FAIL。若 expect_values 未真正接入 checkTable，
// single_row 分支会因无其它断言空 fails 而假 PASS——此测专门锁住该回归。
func TestDataCorrectness_ExpectValuesViaEvaluateWrongValueFails(t *testing.T) {
	resp := contract.Response{Status: contract.StatusOK, Table: &contract.TableResult{
		Columns: []contract.ColumnMeta{{Name: "total_count"}, {Name: "churned_count"}},
		Rows:    [][]any{{int64(20), int64(12)}},
	}}
	res := TaskResult{ToolCalls: []contract.ToolCall{{Name: "analyze", Response: resp}}}
	spec := specNode(t, `
tool: analyze
expect_status: OK
any_of:
  - table:
    - single_row: true
      expect_values:
        - {candidates: ["total", "count", "n"], value: 99999}
`)
	score, err := NewDataCorrectness().Evaluate(context.Background(), res, spec)
	if err != nil {
		t.Fatal(err)
	}
	if score.Pass {
		t.Fatal("值 99999 不在行内，expect_values 已接入则必须 FAIL（防假 PASS 放水）")
	}
}

func TestDataCorrectness_ExpectValuesRejectsEmptyCandidates(t *testing.T) {
	res := resultWith(contract.Response{Status: contract.StatusOK})
	spec := specNode(t, `
tool: query_distribution
table:
  - single_row: true
    expect_values:
      - {candidates: [], value: 20}
`)
	_, err := NewDataCorrectness().Evaluate(context.Background(), res, spec)
	if err == nil {
		t.Fatal("expect_values 空 candidates 应返回配置错误")
	}
}

func TestDataCorrectness_CountChurnBothShapesRealValues(t *testing.T) {
	cases := []struct {
		name                          string
		retain, churn, total, churned int64
	}{
		{"low_creditscore", 12, 18, 30, 18},
		{"new_customer", 6, 14, 20, 14},
		{"high_balance", 8, 12, 20, 12},
	}
	mkSpec := func(retain, churn, total, churned int64) *yaml.Node {
		return specNode(t, fmt.Sprintf(`
tool: analyze
expect_status: OK
any_of:
  - table:
    - match: {Exited: "0"}
      expect_any: [{columns: ["n","count","customer_count","cnt"], value: %d}]
    - match: {Exited: "1"}
      expect_any: [{columns: ["n","count","customer_count","cnt"], value: %d}]
  - table:
    - single_row: true
      expect_any:
        - {columns: ["total","count","n","cnt"], value: %d}
        - {columns: ["churned","sum_Exited","exited_sum","exited"], value: %d}
`, retain, churn, total, churned))
	}
	for _, c := range cases {
		spec := mkSpec(c.retain, c.churn, c.total, c.churned)
		gb := TaskResult{ToolCalls: []contract.ToolCall{{Name: "analyze", Response: contract.Response{
			Status: contract.StatusOK, Table: &contract.TableResult{
				Columns: []contract.ColumnMeta{{Name: "Exited"}, {Name: "n"}},
				Rows:    [][]any{{int64(0), c.retain}, {int64(1), c.churn}},
			}}}}}
		if s, _ := NewDataCorrectness().Evaluate(context.Background(), gb, spec); !s.Pass {
			t.Fatalf("%s group_by shape should pass, got %+v", c.name, s)
		}
		agg := TaskResult{ToolCalls: []contract.ToolCall{{Name: "analyze", Response: contract.Response{
			Status: contract.StatusOK, Table: &contract.TableResult{
				Columns: []contract.ColumnMeta{{Name: "total"}, {Name: "sum_Exited"}},
				Rows:    [][]any{{c.total, c.churned}},
			}}}}}
		if s, _ := NewDataCorrectness().Evaluate(context.Background(), agg, spec); !s.Pass {
			t.Fatalf("%s aggregate shape should pass, got %+v", c.name, s)
		}
		bad := TaskResult{ToolCalls: []contract.ToolCall{{Name: "analyze", Response: contract.Response{
			Status: contract.StatusOK, Table: &contract.TableResult{
				Columns: []contract.ColumnMeta{{Name: "total"}, {Name: "sum_Exited"}},
				Rows:    [][]any{{c.total, c.churned + 1}},
			}}}}}
		if s, _ := NewDataCorrectness().Evaluate(context.Background(), bad, spec); s.Pass {
			t.Fatalf("%s wrong churned should fail", c.name)
		}
	}
}
