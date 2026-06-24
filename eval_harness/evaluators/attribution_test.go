package evaluators

import (
	"testing"

	"github.com/RuntianLee/schema-driven-insight-agent/contract"
)

func twoGroupCalls() []contract.ToolCall {
	return []contract.ToolCall{
		{Name: "analyze", Response: contract.Response{
			Status:  contract.StatusOK,
			Profile: &contract.DistProfile{Count: 1000, Mean: 2500, Median: 1800, P90: 8000},
		}},
		{Name: "analyze", Response: contract.Response{
			Status: contract.StatusOK,
			Groups: []contract.GroupProfile{
				{Group: "EU", Profile: contract.DistProfile{Count: 600, Mean: 3000}},
				{Group: "US", Profile: contract.DistProfile{Count: 400, Mean: 1500}},
			},
			Data: []contract.BucketRow{
				{Bucket: "500-1000", PlayerCount: 120, PctPlayers: 0.12},
			},
			GroupsTail: &contract.GroupsTail{GroupCount: 3, PlayerCount: 80, PctPlayers: 0.08},
		}},
	}
}

func TestResolve_Cells(t *testing.T) {
	calls := twoGroupCalls()
	cases := []struct {
		path string
		want float64
	}{
		{"q1.profile.median", 1800},
		{"q1.profile.p90", 8000},
		{"q2.group[EU].profile.mean", 3000},
		{"q2.group[US].profile.count", 400},
		{"q2.bucket[500-1000].pct_players", 0.12},
		{"q2.bucket[500-1000].player_count", 120}, // int64→float64 roundtrip
		{"q2.groups_tail.player_count", 80},
	}
	for _, c := range cases {
		got, err := Resolve(calls, c.path)
		if err != nil {
			t.Errorf("%s: 意外报错 %v", c.path, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: got %v want %v", c.path, got, c.want)
		}
	}
}

func TestResolve_Unresolvable(t *testing.T) {
	calls := twoGroupCalls()
	for _, path := range []string{
		"q9.profile.mean",           // q 越界
		"q1.profile.nope",           // 字段不存在
		"q2.group[ZZ].profile.mean", // 选择器未命中
		"q1.profile",                // 叶子非数值（对象）
		"q2.table.row[0].col",       // table 寻址（q2 无 table 字段 / 命中 stub）→ Task 2 转正向基准
	} {
		if _, err := Resolve(calls, path); err == nil {
			t.Errorf("%s: 应报错（不可解析）", path)
		}
	}
}

func tableCalls() []contract.ToolCall {
	return []contract.ToolCall{
		{Name: "analyze", Response: contract.Response{
			Status: contract.StatusOK,
			Table: &contract.TableResult{
				Columns:  []contract.ColumnMeta{{Name: "server_id"}, {Name: "avg_money"}},
				Rows:     [][]any{{1, 2000.0}, {2, 8000.0}},
				RowCount: 2,
			},
		}},
	}
}

func TestResolve_TableCell(t *testing.T) {
	calls := tableCalls()
	got, err := Resolve(calls, "q1.table.row[1].avg_money")
	if err != nil {
		t.Fatalf("意外报错: %v", err)
	}
	if got != 8000.0 {
		t.Fatalf("got %v want 8000", got)
	}
}

func TestResolve_TableCell_Bad(t *testing.T) {
	calls := tableCalls()
	for _, path := range []string{
		"q1.table.row[9].avg_money", // 行越界
		"q1.table.row[0].nope",      // 列名不存在
	} {
		if _, err := Resolve(calls, path); err == nil {
			t.Errorf("%s: 应报错", path)
		}
	}
}
