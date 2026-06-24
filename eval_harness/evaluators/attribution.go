// framework/eval_harness/evaluators/attribution.go
package evaluators

import (
	"encoding/json"
	"fmt"
	"regexp"
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
	if n < 1 || n > len(calls) {
		return 0, fmt.Errorf("%s 越界（共 %d 个结果）", segs[0], len(calls))
	}
	var cur any
	b, err := json.Marshal(calls[n-1].Response)
	if err != nil {
		return 0, fmt.Errorf("序列化 Response 失败: %w", err)
	}
	if err := json.Unmarshal(b, &cur); err != nil {
		return 0, fmt.Errorf("JSON 反序列化 Response 失败: %w", err)
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

// navSelector 处理 group[K]/bucket[K] 键控数组查找。
func navSelector(cur any, name, key string, rest []string) (float64, error) {
	cfg, ok := keyedArray[name]
	if !ok {
		return 0, fmt.Errorf("未知选择器 %q", name)
	}
	m, ok := cur.(map[string]any)
	if !ok {
		return 0, fmt.Errorf("选择器 %q 的父节点非对象", name)
	}
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

// toFloat 把 JSON 数值（统一 float64）转出；非数值返回 ok=false。
func toFloat(v any) (float64, bool) {
	f, ok := v.(float64)
	return f, ok
}

// navTableCell 在 Task 2 完整实现；此处先占位保证编译。
func navTableCell(m map[string]any, segs []string) (float64, error) {
	return 0, fmt.Errorf("table 寻址尚未实现")
}
