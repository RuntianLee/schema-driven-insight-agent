package evaluators

import (
	"errors"
	"strings"
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

// TestResolve_SkipsSchemaError 验证 q{N} 只数 status=OK 的结果：
// SCHEMA_ERROR 重试不计数，故 q1 指向第一个成功结果而非失败的那次（2026-06-25 T1 实证）。
func TestResolve_SkipsSchemaError(t *testing.T) {
	calls := []contract.ToolCall{
		{Name: "analyze", Response: contract.Response{Status: contract.StatusSchemaError}},
		tableCalls()[0], // 成功结果（含 table，avg_money 行 1=8000）
	}
	got, err := Resolve(calls, "q1.table.row[1].avg_money")
	if err != nil {
		t.Fatalf("q1 应跳过 SCHEMA_ERROR 指向成功结果，却报错: %v", err)
	}
	if got != 8000.0 {
		t.Fatalf("got %v want 8000", got)
	}
	// 只有 1 个成功结果时 q2 应越界（计数基于 OK 结果数）。
	if _, err := Resolve(calls, "q2.table.row[0].avg_money"); err == nil {
		t.Fatal("q2 应越界（仅 1 个成功结果）")
	}
}

// TestResolve_TableCell_PluralRows：模型镜像字面 JSON 写复数 rows[i]（非单数 row 关键字），
// 须与 row[i] 同样解析（2026-06-25 T1 实证：table 形状任务模型全写 table.rows[i]）。
func TestResolve_TableCell_PluralRows(t *testing.T) {
	calls := tableCalls()
	got, err := Resolve(calls, "q1.table.rows[1].avg_money")
	if err != nil {
		t.Fatalf("复数 rows[i] 应解析: %v", err)
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

// TestOpCatalog_ShowsCallForm：catalog 须用函数调用形式 ratio(a,b) 而非中缀 ratio=，
// 否则模型抄成中缀算式 anchor（2026-06-25 T1 实证：模型把 prompt 的 ratio=a/b 照抄为 ratio=q2.../q1...）。
func TestOpCatalog_ShowsCallForm(t *testing.T) {
	cat := OpCatalog()
	if !strings.Contains(cat, "ratio(") {
		t.Fatalf("OpCatalog 须用函数调用形式 ratio(...)：\n%s", cat)
	}
}

func TestResolveAnchor_Derived(t *testing.T) {
	calls := twoGroupCalls() // q2.group[EU].mean=3000, q2.group[US].mean=1500
	cases := []struct {
		anchor string
		want   float64
	}{
		{"ratio(q2.group[EU].profile.mean, q2.group[US].profile.mean)", 2.0},
		{"diff(q2.group[EU].profile.mean, q2.group[US].profile.mean)", 1500},
		{"pct_change(q2.group[EU].profile.mean, q2.group[US].profile.mean)", 1.0},
		{"pct_points(q2.group[EU].profile.mean, q2.group[US].profile.mean)", 1500},
		{"sum(q2.group[EU].profile.count, q2.group[US].profile.count)", 1000},
	}
	for _, c := range cases {
		got, err := ResolveAnchor(calls, c.anchor)
		if err != nil {
			t.Errorf("%s: 意外报错 %v", c.anchor, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: got %v want %v", c.anchor, got, c.want)
		}
	}
}

func TestResolveAnchor_UnsupportedOp(t *testing.T) {
	calls := twoGroupCalls()
	_, err := ResolveAnchor(calls, "harmonic_mean(q1.profile.mean, q1.profile.median)")
	if !errors.Is(err, errUnsupportedOp) {
		t.Fatalf("未注册算子应返回 errUnsupportedOp，得到 %v", err)
	}
}

func TestResolveAnchor_OperandUnresolvable(t *testing.T) {
	calls := twoGroupCalls()
	if _, err := ResolveAnchor(calls, "ratio(q2.group[ZZ].profile.mean, q1.profile.mean)"); err == nil {
		t.Fatal("操作数不可解析应报错")
	}
}

func TestResolveAnchor_PlainCell(t *testing.T) {
	calls := twoGroupCalls()
	got, err := ResolveAnchor(calls, "q1.profile.median") // 非派生式 → 当单元格路径
	if err != nil || got != 1800 {
		t.Fatalf("普通路径派发失败: got %v err %v", got, err)
	}
}

func TestOpCatalog_ListsOps(t *testing.T) {
	cat := OpCatalog()
	for _, name := range []string{"ratio", "diff", "pct_change", "pct_points", "sum"} {
		if !strings.Contains(cat, name) {
			t.Errorf("算子小抄缺 %q:\n%s", name, cat)
		}
	}
}

func TestEvalAnchor_Statuses(t *testing.T) {
	calls := twoGroupCalls()
	cases := []struct {
		anchor  string
		claimed float64
		want    AttrStatus
	}{
		{"q2.group[EU].profile.mean", 3000, AttrResolved},
		{"q2.group[EU].profile.mean", 9999, AttrMismatch},
		{"q2.groups[0].profile.mean", 3000, AttrResolved}, // 字面下标整链路 resolved
		{"ratio(q2.group[EU].profile.mean, q2.group[US].profile.mean)", 2.0, AttrResolved},
		{"q2.group[ZZ].profile.mean", 1, AttrUnresolvable},
		{"harmonic_mean(q1.profile.mean, q1.profile.median)", 1, AttrDerivUnsupported},
		{"", 1, AttrUnresolvable},
	}
	for _, c := range cases {
		v := EvalAnchor(calls, c.anchor, c.claimed, defaultAttrTol)
		if v.Status != c.want {
			t.Errorf("anchor=%q claimed=%v: got %s want %s", c.anchor, c.claimed, v.Status, c.want)
		}
	}
}

func TestEvalAnchor_ToleranceAndZero(t *testing.T) {
	calls := twoGroupCalls() // q2.group[EU].mean=3000
	if v := EvalAnchor(calls, "q2.group[EU].profile.mean", 2985, defaultAttrTol); v.Status != AttrResolved {
		t.Fatalf("2985 应在容差内 resolved，得到 %s", v.Status)
	}
	zeroCalls := []contract.ToolCall{{Response: contract.Response{Status: contract.StatusOK, Profile: &contract.DistProfile{Mean: 0}}}}
	if v := EvalAnchor(zeroCalls, "q1.profile.mean", 0, defaultAttrTol); v.Status != AttrResolved {
		t.Fatalf("0≈0 应 resolved，得到 %s", v.Status)
	}
}

func TestAttributionRate(t *testing.T) {
	vs := []AttributionVerdict{
		{Status: AttrResolved}, {Status: AttrResolved},
		{Status: AttrMismatch}, {Status: AttrUnresolvable},
	}
	if got := AttributionRate(vs); got != 0.5 {
		t.Fatalf("rate got %v want 0.5", got)
	}
	if got := AttributionRate(nil); got != 0 {
		t.Fatalf("空集 rate 应为 0，得到 %v", got)
	}
}

// groupsWithDataCalls 构造组内带 Data 的结果，复现模型镜像 JSON 的自然锚
// （groups[i].data[j].avg_value）。单 call → q1。
func groupsWithDataCalls() []contract.ToolCall {
	return []contract.ToolCall{
		{Name: "analyze", Response: contract.Response{
			Status: contract.StatusOK,
			Groups: []contract.GroupProfile{
				{Group: "EU", Profile: contract.DistProfile{Count: 600, Mean: 3000}, Data: []contract.BucketRow{
					{Bucket: "0-500", AvgValue: 250},
					{Bucket: "500-1000", AvgValue: 750},
				}},
				{Group: "US", Profile: contract.DistProfile{Count: 400, Mean: 1500}, Data: []contract.BucketRow{
					{Bucket: "0-500", AvgValue: 300},
				}},
			},
			Data: []contract.BucketRow{
				{Bucket: "top", AvgValue: 9000, PctPlayers: 0.05},
			},
		}},
	}
}

func TestResolve_LiteralArrayIndex(t *testing.T) {
	calls := groupsWithDataCalls()
	cases := []struct {
		path string
		want float64
	}{
		{"q1.groups[0].profile.mean", 3000},
		{"q1.groups[1].profile.mean", 1500},
		{"q1.groups[0].data[1].avg_value", 750},
		{"q1.groups[1].data[0].avg_value", 300},
		{"q1.data[0].avg_value", 9000},
		{"q1.data[0].pct_players", 0.05},
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

func TestResolve_LiteralArrayIndex_Bad(t *testing.T) {
	calls := groupsWithDataCalls()
	for _, path := range []string{
		"q1.groups[9].profile.mean",
		"q1.groups[EU].profile.mean",
		"q1.data[5].avg_value",
	} {
		if _, err := Resolve(calls, path); err == nil {
			t.Errorf("%s: 应报错（不可解析）", path)
		}
	}
}

// TestResolve_TableCell_NumericColumnIndex：模型镜像原始 JSON 写数字列下标 rows[i][j]，
// 须与 rows[i].<列名> 等价（2026-06-27 (b') case iii：q2.table.rows[0][1]）。
func TestResolve_TableCell_NumericColumnIndex(t *testing.T) {
	calls := tableCalls()
	cases := []struct {
		path string
		want float64
	}{
		{"q1.table.rows[1][1]", 8000.0},       // 单段双下标：第1行第1列(avg_money)
		{"q1.table.rows[0][0]", 1.0},          // 第0行第0列(server_id)
		{"q1.table.rows[1].1", 8000.0},        // 点分形式数字列位，等价
		{"q1.table.row[1].avg_money", 8000.0}, // 列名形式仍工作（回归）
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

func TestResolve_TableCell_NumericColumnIndex_Bad(t *testing.T) {
	calls := tableCalls()
	for _, path := range []string{
		"q1.table.rows[0][9]", // 列下标越界
		"q1.table.rows[9][0]", // 行下标越界
	} {
		if _, err := Resolve(calls, path); err == nil {
			t.Errorf("%s: 应报错", path)
		}
	}
}
