// framework/eval_harness/evaluators/attribution.go
package evaluators

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/RuntianLee/schema-driven-insight-agent/contract"
)

// selectorRe 匹配 name[key] 形态选择器：group[EU] / bucket[500-1000] / row[3]。
var selectorRe = regexp.MustCompile(`^([a-zA-Z_]+)\[(.+)\]$`)

// keyedArray 把选择器糖映射到「数组字段名 + 匹配字段名」：
// group[K] = groups[] 里 group==K；bucket[K] = data[] 里 bucket==K。
// 这是 Response 形状约定（2 条），不是字段枚举——新增统计字段无需改这里。
var keyedArray = map[string][2]string{
	"group":  {"groups", "group"},
	"bucket": {"data", "bucket"},
}

// Resolve 把单元格路径（q{N}.<导航>）解析成一个标量数值。
// q{N} 用 contract.AnalystResultID 同口径（1-based，定位 calls[N-1].Response）。
// 任何解析失败/越界/键缺失/叶子非数值都返回明确 error（调用方据此标 unresolvable）。
func Resolve(calls []contract.ToolCall, path string) (float64, error) {
	segs := strings.Split(path, ".")
	if len(segs) < 2 {
		return 0, fmt.Errorf("path 太短: %q", path)
	}
	n, err := parseQ(segs[0])
	if err != nil {
		return 0, err
	}
	// q{N} 只数 status=OK 的结果：SCHEMA_ERROR/INSUFFICIENT_DATA 等失败重试不计数，
	// 否则一次 schema 重试就会把后续所有锚的编号顶偏（2026-06-25 T1 实证）。
	ok := okCalls(calls)
	if n < 1 || n > len(ok) {
		return 0, fmt.Errorf("%s 越界（共 %d 个成功结果）", segs[0], len(ok))
	}
	var cur any
	b, err := json.Marshal(ok[n-1].Response)
	if err != nil {
		return 0, fmt.Errorf("序列化 Response 失败: %w", err)
	}
	if err := json.Unmarshal(b, &cur); err != nil {
		return 0, fmt.Errorf("JSON 反序列化 Response 失败: %w", err)
	}
	return navigate(cur, segs[1:])
}

// okCalls 过滤出 status=OK 的工具结果，供 q{N} 索引（失败/重试不计数，编号对重试鲁棒）。
func okCalls(calls []contract.ToolCall) []contract.ToolCall {
	out := make([]contract.ToolCall, 0, len(calls))
	for _, c := range calls {
		if c.Response.Status == contract.StatusOK {
			out = append(out, c)
		}
	}
	return out
}

// parseQ 解析 "q3" → 3。
func parseQ(s string) (int, error) {
	if !strings.HasPrefix(s, "q") {
		return 0, fmt.Errorf("首段须为 q{N}: %q", s)
	}
	n, err := strconv.Atoi(s[1:])
	if err != nil {
		return 0, fmt.Errorf("q{N} 无效: %q", s)
	}
	return n, nil
}

// navigate 沿剩余路径段在通用 JSON 结构上导航到标量。
func navigate(cur any, segs []string) (float64, error) {
	if len(segs) == 0 {
		if f, ok := toFloat(cur); ok {
			return f, nil
		}
		return 0, fmt.Errorf("叶子非数值: %v", cur)
	}
	// 表格特例：当前是 table（含 columns+rows），后续段 row[i].<col> 一并消费。
	if m, ok := cur.(map[string]any); ok {
		if _, hasCols := m["columns"]; hasCols {
			if _, hasRows := m["rows"]; hasRows {
				return navTableCell(m, segs)
			}
		}
	}
	seg := segs[0]
	if mm := selectorRe.FindStringSubmatch(seg); mm != nil {
		return navSelector(cur, mm[1], mm[2], segs[1:])
	}
	m, ok := cur.(map[string]any)
	if !ok {
		return 0, fmt.Errorf("段 %q 的父节点非对象", seg)
	}
	child, ok := m[seg]
	if !ok {
		return 0, fmt.Errorf("字段 %q 不存在", seg)
	}
	return navigate(child, segs[1:])
}

// navSelector 处理 name[token] 选择器，按解析顺序：
//  1) group[键]/bucket[键] 语义糖：按键匹配（旧行为，零改动）。
//  2) 字面 JSON-path：name 是当前节点的数组字段、token 是非负整数 → 按下标取元素
//     （模型镜像工具结果 JSON 的自然写法，如 groups[1].data[0].avg_value）。
//  3) 都不符合 → 明确报错（不静默走错）。
func navSelector(cur any, name, key string, rest []string) (float64, error) {
	m, ok := cur.(map[string]any)
	if !ok {
		return 0, fmt.Errorf("选择器 %q 的父节点非对象", name)
	}
	// 1) 语义糖（group/bucket）：按键值匹配数组元素。
	if cfg, ok := keyedArray[name]; ok {
		arr, ok := m[cfg[0]].([]any)
		if !ok {
			return 0, fmt.Errorf("字段 %q 非数组", cfg[0])
		}
		for _, e := range arr {
			em, ok := e.(map[string]any)
			if ok && fmt.Sprint(em[cfg[1]]) == key {
				return navigate(em, rest)
			}
		}
		return 0, fmt.Errorf("%s[%s] 未找到", name, key)
	}
	// 2) 字面数组下标：当前节点存在同名数组字段，且 token 为非负整数。
	if arr, ok := m[name].([]any); ok {
		i, err := strconv.Atoi(key)
		if err != nil {
			return 0, fmt.Errorf("数组字段 %q 的下标须为整数，得到 %q", name, key)
		}
		if i < 0 || i >= len(arr) {
			return 0, fmt.Errorf("%s[%d] 越界（共 %d 个元素）", name, i, len(arr))
		}
		return navigate(arr[i], rest)
	}
	// 3) 既非 group/bucket 糖，当前节点也无同名数组字段。
	return 0, fmt.Errorf("未知选择器 %q（非 group/bucket 糖，且当前节点无同名数组字段）", name)
}

// toFloat 把 JSON 数值（统一 float64）转出；非数值返回 ok=false。
func toFloat(v any) (float64, bool) {
	f, ok := v.(float64)
	return f, ok
}

// navTableCell 消费 row[i].<col> 两段：rows[i] 取行，columns 把列名映射到列下标。
func navTableCell(m map[string]any, segs []string) (float64, error) {
	if len(segs) != 2 {
		return 0, fmt.Errorf("table 寻址须为 row[i].<col>，得到 %v", segs)
	}
	sel := selectorRe.FindStringSubmatch(segs[0])
	// 接受 row[i]（关键字）与 rows[i]（字面 JSON 字段名）——模型倾向镜像结果里的复数 rows。
	if sel == nil || (sel[1] != "row" && sel[1] != "rows") {
		return 0, fmt.Errorf("table 首段须为 row[i] 或 rows[i]: %q", segs[0])
	}
	i, err := strconv.Atoi(sel[2])
	if err != nil {
		return 0, fmt.Errorf("行下标无效: %q", sel[2])
	}
	rows, _ := m["rows"].([]any)
	if i < 0 || i >= len(rows) {
		return 0, fmt.Errorf("行下标 %d 越界（共 %d 行）", i, len(rows))
	}
	row, ok := rows[i].([]any)
	if !ok {
		return 0, fmt.Errorf("第 %d 行非数组", i)
	}
	cols, _ := m["columns"].([]any)
	col := segs[1]
	for ci, c := range cols {
		cm, ok := c.(map[string]any)
		if ok && fmt.Sprint(cm["name"]) == col {
			if ci >= len(row) {
				return 0, fmt.Errorf("列 %q 下标 %d 超出行宽", col, ci)
			}
			if f, ok := toFloat(row[ci]); ok {
				return f, nil
			}
			return 0, fmt.Errorf("单元格 [%d][%s] 非数值: %v", i, col, row[ci])
		}
	}
	return 0, fmt.Errorf("列 %q 不存在", col)
}

var errUnsupportedOp = errors.New("派生算子未注册")

// DerivOp 是一个派生算子：纯函数 + arity 声明 + 喂判官的一句话语义。
// Arity = -1 表示变长（≥1）。
type DerivOp struct {
	Name  string
	Arity int
	Apply func([]float64) (float64, error)
	Doc   string
}

var derivOps = map[string]DerivOp{}

// RegisterOp 注册一个派生算子（单一真值源：判官小抄由此自动生成）。
func RegisterOp(op DerivOp) { derivOps[op.Name] = op }

func init() {
	RegisterOp(DerivOp{Name: "ratio", Arity: 2, Doc: "a/b 倍数", Apply: func(v []float64) (float64, error) { return divide(v[0], v[1]) }})
	RegisterOp(DerivOp{Name: "pct", Arity: 2, Doc: "a/b 占比", Apply: func(v []float64) (float64, error) { return divide(v[0], v[1]) }})
	RegisterOp(DerivOp{Name: "diff", Arity: 2, Doc: "a−b 绝对差", Apply: func(v []float64) (float64, error) { return v[0] - v[1], nil }})
	RegisterOp(DerivOp{Name: "pct_points", Arity: 2, Doc: "a−b 两百分比相减（百分点）", Apply: func(v []float64) (float64, error) { return v[0] - v[1], nil }})
	RegisterOp(DerivOp{Name: "spread", Arity: 2, Doc: "a−b 分位/离散度差", Apply: func(v []float64) (float64, error) { return v[0] - v[1], nil }})
	RegisterOp(DerivOp{Name: "pct_change", Arity: 2, Doc: "(a−b)/b 相对变化", Apply: func(v []float64) (float64, error) {
		d, err := divide(v[0]-v[1], v[1])
		return d, err
	}})
	RegisterOp(DerivOp{Name: "sum", Arity: -1, Doc: "求和（变长）", Apply: func(v []float64) (float64, error) {
		var s float64
		for _, x := range v {
			s += x
		}
		return s, nil
	}})
}

func divide(a, b float64) (float64, error) {
	if b == 0 {
		return 0, fmt.Errorf("除零")
	}
	return a / b, nil
}

// derivRe 匹配派生式 name(args)；args 内为逗号分隔的单元格路径（Phase 1 不支持嵌套括号）。
var derivRe = regexp.MustCompile(`^([a-zA-Z_]+)\((.*)\)$`)

// ResolveAnchor 派发：派生式 name(...) → 解析操作数 + 应用算子；否则当单元格路径走 Resolve。
func ResolveAnchor(calls []contract.ToolCall, anchor string) (float64, error) {
	m := derivRe.FindStringSubmatch(strings.TrimSpace(anchor))
	if m == nil {
		return Resolve(calls, anchor)
	}
	op, ok := derivOps[m[1]]
	if !ok {
		return 0, fmt.Errorf("%w: %s", errUnsupportedOp, m[1])
	}
	args := splitArgs(m[2])
	if op.Arity >= 0 && len(args) != op.Arity {
		return 0, fmt.Errorf("算子 %s 需 %d 个操作数，得到 %d", op.Name, op.Arity, len(args))
	}
	if op.Arity == -1 && len(args) == 0 {
		return 0, fmt.Errorf("算子 %s 需至少 1 个操作数", op.Name)
	}
	vals := make([]float64, len(args))
	for i, a := range args {
		v, err := Resolve(calls, strings.TrimSpace(a))
		if err != nil {
			return 0, fmt.Errorf("操作数 %q 不可解析: %w", a, err)
		}
		vals[i] = v
	}
	return op.Apply(vals)
}

// splitArgs 按顶层逗号切分（Phase 1 操作数是无嵌套括号的路径，简单切分即可）。
func splitArgs(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.TrimSpace(p))
	}
	return out
}

// defaultAttrTol 是默认相对容差（吸收 narrative 四舍五入）。
const defaultAttrTol = 0.01

// AttrStatus 是单条主张的确定性裁决。
type AttrStatus string

const (
	AttrResolved         AttrStatus = "resolved"            // 锚解析通 + 值匹配
	AttrMismatch         AttrStatus = "mismatch"            // 解析通但值不符（疑似幻觉）
	AttrUnresolvable     AttrStatus = "unresolvable"        // 锚解析不出（含空锚 / 判官提错锚）
	AttrDerivUnsupported AttrStatus = "derived_unsupported" // 真派生但算子未注册 → 回退判官软评
)

// AttributionVerdict 是 resolver 对单条主张的裁决记录。
type AttributionVerdict struct {
	Anchor   string
	Status   AttrStatus
	Resolved float64 // 仅 resolved/mismatch 有意义
	Claimed  float64
}

// EvalAnchor 解析锚并与判官读到的数值（claimed）按相对容差比对，给出确定性裁决。
func EvalAnchor(calls []contract.ToolCall, anchor string, claimed, relTol float64) AttributionVerdict {
	v := AttributionVerdict{Anchor: anchor, Claimed: claimed}
	if strings.TrimSpace(anchor) == "" {
		v.Status = AttrUnresolvable
		return v
	}
	val, err := ResolveAnchor(calls, anchor)
	if err != nil {
		if errors.Is(err, errUnsupportedOp) {
			v.Status = AttrDerivUnsupported
		} else {
			v.Status = AttrUnresolvable
		}
		return v
	}
	v.Resolved = val
	if relClose(val, claimed, relTol) {
		v.Status = AttrResolved
	} else {
		v.Status = AttrMismatch
	}
	return v
}

// relClose: claimed≈0 时退化为绝对容差，避免除零。
func relClose(got, want, relTol float64) bool {
	if math.Abs(want) < 1e-9 {
		return math.Abs(got) < 1e-9
	}
	return math.Abs(got-want)/math.Abs(want) <= relTol
}

// AttributionRate = resolved 数 / 总主张数；空集为 0。
func AttributionRate(vs []AttributionVerdict) float64 {
	if len(vs) == 0 {
		return 0
	}
	var n int
	for _, v := range vs {
		if v.Status == AttrResolved {
			n++
		}
	}
	return float64(n) / float64(len(vs))
}

// OpCatalog 从注册表生成判官小抄（名字稳定排序，便于 prompt 复现）。
func OpCatalog() string {
	names := make([]string, 0, len(derivOps))
	for n := range derivOps {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for i, n := range names {
		if i > 0 {
			b.WriteString("; ")
		}
		// 函数调用形式 name(args)：doc，避免被抄成中缀 name=expr（2026-06-25 T1 实证）。
		fmt.Fprintf(&b, "%s(%s)：%s", n, opArgPlaceholder(derivOps[n].Arity), derivOps[n].Doc)
	}
	return b.String()
}

// opArgPlaceholder 按 arity 给算子参数占位：变长用「…」，定长用 a,b,c…。
func opArgPlaceholder(arity int) string {
	if arity < 0 {
		return "…"
	}
	parts := make([]string, 0, arity)
	for i := 0; i < arity; i++ {
		parts = append(parts, string(rune('a'+i)))
	}
	return strings.Join(parts, ",")
}
