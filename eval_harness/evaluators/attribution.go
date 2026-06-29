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

// rowsIJRe 匹配单段双下标 rows[i][j] / row[i][j]：数字行下标 + 数字列下标
// （模型镜像原始 JSON 数组的自然写法，2026-06-27 (b') case iii）。
var rowsIJRe = regexp.MustCompile(`^rows?\[(\d+)\]\[(\d+)\]$`)

// rowsStarRe 匹配整列通配段 rows[*] / row[*]（列聚合，2026-06-27 (b'')）。
var rowsStarRe = regexp.MustCompile(`^rows?\[\*\]$`)

// rowsStarJRe 匹配单段整列通配+数字列下标 rows[*][j] / row[*][j]（2026-06-27 (b'')）。
var rowsStarJRe = regexp.MustCompile(`^rows?\[\*\]\[(\d+)\]$`)

// keyedArray 把选择器糖映射到「数组字段名 + 匹配字段名」：
// group[K] = groups[] 里 group==K；bucket[K] = data[] 里 bucket==K。
// 这是 Response 形状约定（2 条），不是字段枚举——新增统计字段无需改这里。
var keyedArray = map[string][2]string{
	"group":  {"groups", "group"},
	"bucket": {"data", "bucket"},
}

// qNode 解析 q{N} 首段，返回该成功结果 Response 的通用 JSON 根（map/any）。
// q{N} 用 contract.OKCalls 过滤后 1-based 定位（与 AnalystResults/advisor_grounding 同口径）。
// Resolve（标量）与 resolveColumn（向量）共用此前缀逻辑（DRY）。
func qNode(calls []contract.ToolCall, qseg string) (any, error) {
	n, err := parseQ(qseg)
	if err != nil {
		return nil, err
	}
	ok := contract.OKCalls(calls)
	if n < 1 || n > len(ok) {
		return nil, fmt.Errorf("%s 越界（共 %d 个成功结果）", qseg, len(ok))
	}
	var cur any
	b, err := json.Marshal(ok[n-1].Response)
	if err != nil {
		return nil, fmt.Errorf("序列化 Response 失败: %w", err)
	}
	if err := json.Unmarshal(b, &cur); err != nil {
		return nil, fmt.Errorf("JSON 反序列化 Response 失败: %w", err)
	}
	return cur, nil
}

// Resolve 把单元格路径（q{N}.<导航>）解析成一个标量数值。
// q{N} 用 contract.OKCalls 过滤后 1-based 定位（与 AnalystResults/advisor_grounding 同口径，
// 2026-06-27 (b') 统一）；失败/重试调用不计数。
// 任何解析失败/越界/键缺失/叶子非数值都返回明确 error（调用方据此标 unresolvable）。
func Resolve(calls []contract.ToolCall, path string) (float64, error) {
	segs := strings.Split(path, ".")
	if len(segs) < 2 {
		return 0, fmt.Errorf("path 太短: %q", path)
	}
	cur, err := qNode(calls, segs[0])
	if err != nil {
		return 0, err
	}
	return navigate(cur, segs[1:])
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

// navTableCell 解析 table 单元格。支持三种列位写法（数字下标与列名同权）：
//   rows[i].<列名>（两段，列名）/ rows[i].<j>（两段，数字列下标）/ rows[i][j]（单段，数字列下标）。
// 接受 row[i]（关键字）与 rows[i]（字面 JSON 字段名）——模型倾向镜像结果里的复数 rows。
func navTableCell(m map[string]any, segs []string) (float64, error) {
	rows, hasRows := m["rows"].([]any)
	if !hasRows {
		return 0, fmt.Errorf("table 缺少 rows 字段或类型错误")
	}
	switch len(segs) {
	case 1:
		ij := rowsIJRe.FindStringSubmatch(segs[0])
		if ij == nil {
			return 0, fmt.Errorf("table 单段寻址须为 rows[i][j]: %q", segs[0])
		}
		// rowsIJRe 已保证 ij[1]/ij[2] 为 \d+，Atoi 不会失败。
		i, _ := strconv.Atoi(ij[1])
		j, _ := strconv.Atoi(ij[2])
		row, err := tableRow(rows, i)
		if err != nil {
			return 0, err
		}
		return tableCellByPos(row, i, j)
	case 2:
		sel := selectorRe.FindStringSubmatch(segs[0])
		if sel == nil || (sel[1] != "row" && sel[1] != "rows") {
			return 0, fmt.Errorf("table 首段须为 row[i] 或 rows[i]: %q", segs[0])
		}
		i, err := strconv.Atoi(sel[2])
		if err != nil {
			return 0, fmt.Errorf("行下标无效: %q", sel[2])
		}
		row, err := tableRow(rows, i)
		if err != nil {
			return 0, err
		}
		col := segs[1]
		if j, err := strconv.Atoi(col); err == nil {
			return tableCellByPos(row, i, j)
		}
		cols, _ := m["columns"].([]any)
		for ci, c := range cols {
			cm, ok := c.(map[string]any)
			if ok && fmt.Sprint(cm["name"]) == col {
				return tableCellByPos(row, i, ci)
			}
		}
		return 0, fmt.Errorf("列 %q 不存在", col)
	default:
		return 0, fmt.Errorf("table 寻址须为 row[i].<col> 或 rows[i][j]，得到 %v", segs)
	}
}

// tableRow 取第 i 行（边界检查 + 行须为数组）。
func tableRow(rows []any, i int) ([]any, error) {
	if i < 0 || i >= len(rows) {
		return nil, fmt.Errorf("行下标 %d 越界（共 %d 行）", i, len(rows))
	}
	row, ok := rows[i].([]any)
	if !ok {
		return nil, fmt.Errorf("第 %d 行非数组", i)
	}
	return row, nil
}

// tableCellByPos 取第 i 行第 j 列的标量（列下标越界 / 非数值 → 明确报错）。
func tableCellByPos(row []any, i, j int) (float64, error) {
	if j < 0 || j >= len(row) {
		return 0, fmt.Errorf("列下标 %d 越界（行宽 %d）", j, len(row))
	}
	if f, ok := toFloat(row[j]); ok {
		return f, nil
	}
	return 0, fmt.Errorf("单元格 [%d][%d] 非数值: %v", i, j, row[j])
}

// resolveColumn 解析含 rows[*] 的路径为一列标量向量（仅供变长算子如 sum 消费）。
// 路径形如 q{N}.<...>.table.rows[*].<列名|数字列下标>。
func resolveColumn(calls []contract.ToolCall, path string) ([]float64, error) {
	segs := strings.Split(path, ".")
	if len(segs) < 2 {
		return nil, fmt.Errorf("path 太短: %q", path)
	}
	cur, err := qNode(calls, segs[0])
	if err != nil {
		return nil, err
	}
	return navColumn(cur, segs[1:])
}

// navColumn 沿通用 JSON 下行直到命中 table（含 columns+rows），再交 navTableColumn 取整列。
func navColumn(cur any, segs []string) ([]float64, error) {
	for i := 0; i < len(segs); i++ {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("段 %q 的父节点非对象", segs[i])
		}
		if _, hasCols := m["columns"]; hasCols {
			if _, hasRows := m["rows"]; hasRows {
				// 单段 rows[*][j] 形式：展开为两段 ["rows[*]", "j"] 交 navTableColumn。
				tail := segs[i:]
				if len(tail) == 1 {
					if mm := rowsStarJRe.FindStringSubmatch(tail[0]); mm != nil {
						return navTableColumn(m, []string{"rows[*]", mm[1]})
					}
				}
				return navTableColumn(m, tail)
			}
		}
		child, ok := m[segs[i]]
		if !ok {
			return nil, fmt.Errorf("字段 %q 不存在", segs[i])
		}
		cur = child
	}
	return nil, fmt.Errorf("路径未到达含 rows[*] 的 table: %v", segs)
}

// navTableColumn 从 table 取 rows[*].<col> 整列为 []float64。
// segs 须为 [rows[*], <列名|数字列下标>]；任意行非数组/单元格非数值/列缺失/越界 → honest error。
func navTableColumn(m map[string]any, segs []string) ([]float64, error) {
	if len(segs) != 2 || !rowsStarRe.MatchString(segs[0]) {
		return nil, fmt.Errorf("rows[*] 列聚合须为 rows[*].<列>，得到 %v", segs)
	}
	rows, ok := m["rows"].([]any)
	if !ok {
		return nil, fmt.Errorf("table 缺少 rows 字段或类型错误")
	}
	col := segs[1]
	j := -1
	if idx, err := strconv.Atoi(col); err == nil {
		j = idx
	} else {
		cols, _ := m["columns"].([]any)
		for ci, c := range cols {
			if cm, ok := c.(map[string]any); ok && fmt.Sprint(cm["name"]) == col {
				j = ci
				break
			}
		}
		if j < 0 {
			return nil, fmt.Errorf("列 %q 不存在", col)
		}
	}
	out := make([]float64, 0, len(rows))
	for i, r := range rows {
		row, ok := r.([]any)
		if !ok {
			return nil, fmt.Errorf("第 %d 行非数组", i)
		}
		if j < 0 || j >= len(row) {
			return nil, fmt.Errorf("列下标 %d 越界（行宽 %d）", j, len(row))
		}
		f, ok := toFloat(row[j])
		if !ok {
			return nil, fmt.Errorf("单元格 [%d][%d] 非数值: %v", i, j, row[j])
		}
		out = append(out, f)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("rows[*] 命中空列")
	}
	return out, nil
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

// derivRe 匹配派生式 name(args)；args 由 splitArgs 括号感知切分，支持嵌套调用。
var derivRe = regexp.MustCompile(`^([a-zA-Z_]+)\((.*)\)$`)

// rowsStarPathRe 检测「裸路径」是否含 rows[*] 通配（用于操作数派发，非算子表达式）。
var rowsStarPathRe = regexp.MustCompile(`rows?\[\*\]`)

// ResolveAnchor 派发：派生式 name(...) → 解析操作数 + 应用算子；否则当单元格路径走 Resolve。
// 操作数经 resolveOperand 求值，支持任意深度嵌套（操作数本身可为 name(...)）与
// rows[*] 列向量（仅可喂变长算子）。
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
	if len(args) == 0 {
		return 0, fmt.Errorf("算子 %s 需至少 1 个操作数", op.Name)
	}
	var vals []float64
	for _, a := range args {
		ov, err := resolveOperand(calls, a)
		if err != nil {
			return 0, fmt.Errorf("操作数 %q 不可解析: %w", a, err)
		}
		// 定长算子：每个操作数必须求成恰好 1 个标量（rows[*] 向量喂定长算子在此被拒）。
		if op.Arity >= 0 && len(ov) != 1 {
			return 0, fmt.Errorf("算子 %s 的操作数 %q 须为标量，得到 %d 个值", op.Name, a, len(ov))
		}
		vals = append(vals, ov...)
	}
	// 定长算子：操作数总数须等于 arity（变长拼接后由 Apply 自行处理）。
	if op.Arity >= 0 && len(vals) != op.Arity {
		return 0, fmt.Errorf("算子 %s 需 %d 个操作数，得到 %d", op.Name, op.Arity, len(vals))
	}
	return op.Apply(vals)
}

// resolveOperand 把一个操作数表达式求成标量向量（标量 → 单元素）：
//  1) name(...) 形态 → 递归 ResolveAnchor（标量结果，支持任意深度嵌套）。
//  2) 裸路径含 rows[*] → resolveColumn（列向量，仅变长算子可消费）。
//  3) 否则 → Resolve（单元格标量）。
//
// 顺序保证 sum(rows[*].x) 这类「含 rows[*] 的算子」走 (1) 而非 (2)。
func resolveOperand(calls []contract.ToolCall, expr string) ([]float64, error) {
	expr = strings.TrimSpace(expr)
	if derivRe.MatchString(expr) {
		v, err := ResolveAnchor(calls, expr)
		if err != nil {
			return nil, err
		}
		return []float64{v}, nil
	}
	if rowsStarPathRe.MatchString(expr) {
		return resolveColumn(calls, expr)
	}
	v, err := Resolve(calls, expr)
	if err != nil {
		return nil, err
	}
	return []float64{v}, nil
}

// splitArgs 按「顶层」逗号切分操作数：维护 () 与 [] 的嵌套深度，
// 只在两者深度均为 0 的逗号处切分，从而正确切出嵌套算子调用（sum(b,c)）与
// 带下标的路径（rows[0][1]）。解析失败一律由上层落 honest unresolvable。
func splitArgs(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	depth := 0
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(', '[':
			depth++
		case ')', ']':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(s[start:i]))
				start = i + 1
			}
		}
	}
	out = append(out, strings.TrimSpace(s[start:]))
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
	if math.IsNaN(claimed) { // claimed 不可解析（倍率词等）→ 无从比对 → unresolvable（不静默、不冒充 mismatch）
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
