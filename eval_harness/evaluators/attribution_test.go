package evaluators

import (
	"encoding/json"
	"errors"
	"math"
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

func TestResolveColumn_RowsWildcard(t *testing.T) {
	calls := tableCalls() // 列 avg_money(列下标1) = [2000, 8000]
	// 列名形式
	got, err := resolveColumn(calls, "q1.table.rows[*].avg_money")
	if err != nil {
		t.Fatalf("列名形式意外报错: %v", err)
	}
	if len(got) != 2 || got[0] != 2000 || got[1] != 8000 {
		t.Fatalf("got %v want [2000 8000]", got)
	}
	// 数字列下标形式
	got2, err := resolveColumn(calls, "q1.table.rows[*][1]")
	if err != nil {
		t.Fatalf("数字列下标意外报错: %v", err)
	}
	if len(got2) != 2 || got2[0] != 2000 || got2[1] != 8000 {
		t.Fatalf("got %v want [2000 8000]", got2)
	}
}

func TestResolveColumn_Bad(t *testing.T) {
	calls := tableCalls()
	for _, path := range []string{
		"q1.table.rows[*].nope", // 列名不存在
		"q1.table.rows[*][9]",   // 列下标越界
		"q9.table.rows[*][1]",   // q 越界
	} {
		if _, err := resolveColumn(calls, path); err == nil {
			t.Errorf("%s: 应报错", path)
		}
	}
}

// churnTableCalls：单 call（q1），table 含 total_customers / churned_count 两列三行。
// 列 total_customers = [100, 200, 300]（和 600）；churned_count = [10, 40, 50]（和 100）。
func churnTableCalls() []contract.ToolCall {
	return []contract.ToolCall{
		{Name: "analyze", Response: contract.Response{
			Status: contract.StatusOK,
			Table: &contract.TableResult{
				Columns:  []contract.ColumnMeta{{Name: "total_customers"}, {Name: "churned_count"}},
				Rows:     [][]any{{100.0, 10.0}, {200.0, 40.0}, {300.0, 50.0}},
				RowCount: 3,
			},
		}},
	}
}

func TestResolveAnchor_Nested(t *testing.T) {
	calls := churnTableCalls()
	cases := []struct {
		anchor string
		want   float64
	}{
		// 占比：第0行 total / 三行 total 之和 = 100/600
		{"ratio(q1.table.rows[0][0], sum(q1.table.rows[0][0], q1.table.rows[1][0], q1.table.rows[2][0]))", 100.0 / 600.0},
		// 两段差距：pct(churned0,total0) - pct(churned2,total2) = 10/100 - 50/300
		{"diff(pct(q1.table.rows[0][1], q1.table.rows[0][0]), pct(q1.table.rows[2][1], q1.table.rows[2][0]))", 10.0/100.0 - 50.0/300.0},
		// rows[*] 整列求和喂 sum
		{"sum(q1.table.rows[*].churned_count)", 100.0},
		// 嵌套 sum + rows[*]：总流失率 = 总churned / 总total = 100/600
		{"ratio(sum(q1.table.rows[*].churned_count), sum(q1.table.rows[*].total_customers))", 100.0 / 600.0},
	}
	const ulpTol = 1e-14 // 仅吸收编译期常量 vs 运行时 float64 的末位差（~1e-17 量级），远小于业务容差
	for _, c := range cases {
		got, err := ResolveAnchor(calls, c.anchor)
		if err != nil {
			t.Errorf("%s: 意外报错 %v", c.anchor, err)
			continue
		}
		diff := got - c.want
		if diff < -ulpTol || diff > ulpTol {
			t.Errorf("%s: got %.20f want %.20f (diff %e)", c.anchor, got, c.want, diff)
		}
	}
}

func TestResolveAnchor_NestedSafety(t *testing.T) {
	calls := churnTableCalls()
	for _, anchor := range []string{
		"ratio(q1.table.rows[*].churned_count, q1.table.rows[*].total_customers)", // 向量喂定长算子 → 操作数非标量
		"q1.table.rows[*].churned_count",                                          // 裸 rows[*]（不在算子内，走标量 Resolve）
		"ratio(diff(1, q1.table.rows[0][0]), diff(q1.table.rows[1][0], q1.table.rows[2][0]), q1.table.rows[0][1])", // 真 arity 错：3 个操作数喂 ratio
	} {
		if _, err := ResolveAnchor(calls, anchor); err == nil {
			t.Errorf("%s: 应报错（unresolvable）", anchor)
		}
	}
}

// TestResolveAnchor_DeepNestingUnresolvableNotStackOverflow 锁 L-2：ResolveAnchor/
// resolveOperand/navigate 互递归须有深度上限（护栏，不动值比对逻辑一个字）。构造深度 25 的
// sum(sum(sum(...q1.table.rows[0][0]...))) 嵌套锚，超过上限须判 unresolvable（明确 error，
// 走既有 unresolvable 语义），不得栈溢出/panic。
func TestResolveAnchor_DeepNestingUnresolvableNotStackOverflow(t *testing.T) {
	calls := churnTableCalls()
	const depth = 25
	anchor := "q1.table.rows[0][0]"
	for i := 0; i < depth; i++ {
		anchor = "sum(" + anchor + ")"
	}
	if _, err := ResolveAnchor(calls, anchor); err == nil {
		t.Fatal("深度 25 嵌套应判 unresolvable（error），未报错")
	}
}

func TestSplitArgs_BracketAware(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"a, b", []string{"a", "b"}},
		{"q1.x", []string{"q1.x"}},
		{"", nil},
		{"a, sum(b, c, d)", []string{"a", "sum(b, c, d)"}},            // 嵌套括号内逗号不切
		{"pct(a, b), pct(c, d)", []string{"pct(a, b)", "pct(c, d)"}}, // 两个嵌套操作数
		{"q1.table.rows[0][1], q1.table.rows[1][1]", []string{"q1.table.rows[0][1]", "q1.table.rows[1][1]"}}, // 方括号深度跟踪覆盖
		{"ratio(diff(1, x), diff(y, z))", []string{"ratio(diff(1, x), diff(y, z))"}}, // 整体单 arg（深度从不归 0）
	}
	for _, c := range cases {
		got := splitArgs(c.in)
		if len(got) != len(c.want) {
			t.Errorf("splitArgs(%q) = %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("splitArgs(%q)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

// TestEvalAnchor_Conservation_Nested 守恒不变量：嵌套/rows[*] 锚解析通后，
// claimed 与真值不符仍必须判 AttrMismatch（查假数能力零回归）；相符判 AttrResolved。
func TestEvalAnchor_Conservation_Nested(t *testing.T) {
	calls := churnTableCalls() // 总流失率真值 = 100/600 ≈ 0.1667
	cases := []struct {
		anchor  string
		claimed float64
		want    AttrStatus
	}{
		{"ratio(sum(q1.table.rows[*].churned_count), sum(q1.table.rows[*].total_customers))", 100.0 / 600.0, AttrResolved},
		{"ratio(sum(q1.table.rows[*].churned_count), sum(q1.table.rows[*].total_customers))", 0.5, AttrMismatch}, // 编造值仍被抓
		{"sum(q1.table.rows[*].churned_count)", 100, AttrResolved},
		{"sum(q1.table.rows[*].churned_count)", 999, AttrMismatch}, // 编造值仍被抓
	}
	for _, c := range cases {
		v := EvalAnchor(calls, c.anchor, c.claimed, defaultAttrTol)
		if v.Status != c.want {
			t.Errorf("anchor=%q claimed=%v: got %s want %s", c.anchor, c.claimed, v.Status, c.want)
		}
	}
}

// TestEvalAnchor_NaNClaimedUnresolvable 锁死：claimed 不可解析（NaN，如倍率词）→ unresolvable，不静默、不冒充 mismatch。
func TestEvalAnchor_NaNClaimedUnresolvable(t *testing.T) {
	calls := twoGroupCalls() // q2.group[EU].profile.mean=3000
	v := EvalAnchor(calls, "q2.group[EU].profile.mean", math.NaN(), defaultAttrTol)
	if v.Status != AttrUnresolvable {
		t.Fatalf("NaN claimed 应 unresolvable，得到 %s", v.Status)
	}
}

// TestClaimAnchor_StringClaimedConservation 守恒矩阵（端到端：JSON 字符串 claimed → EvalAnchor 裁决）。
// 锚对→resolved；锚错→仍 mismatch（编造仍抓，判定零放水）。
func TestClaimAnchor_StringClaimedConservation(t *testing.T) {
	calls := twoGroupCalls() // EU.mean=3000, US.mean=1500
	cases := []struct {
		name    string
		rawJSON string // 单条 claim JSON（claimed_value 为字符串）
		anchor  string
		want    AttrStatus
	}{
		{"带单位串_锚对", `{"claimed_value":"3000人"}`, "q2.group[EU].profile.mean", AttrResolved},
		{"带单位串_锚错", `{"claimed_value":"3000人"}`, "q2.group[US].profile.mean", AttrMismatch},
		{"倍率词_未支持", `{"claimed_value":"20万"}`, "q2.group[EU].profile.mean", AttrUnresolvable},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var anchor struct {
				ClaimedValue contract.ClaimedNumber `json:"claimed_value"`
			}
			if err := json.Unmarshal([]byte(c.rawJSON), &anchor); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			v := EvalAnchor(calls, c.anchor, float64(anchor.ClaimedValue), defaultAttrTol)
			if v.Status != c.want {
				t.Fatalf("got %s want %s", v.Status, c.want)
			}
		})
	}
}
