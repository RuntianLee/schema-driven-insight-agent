// analysis_query.go 定义通用 analyze 工具的扁平声明式查询请求（V2 路线 B）。
// 与 DistQuery 并列；framework 校验白名单 + 参数化构造 SQL（design-v3 §7 不变量）。
// 仅截面分析：无 join / 无时序 / 无统计窗口 / 无嵌套（spec K2/K6）。
package schema_protocol

import "regexp"

// AnalysisQuery 是 analyze 工具的内部查询请求。
type AnalysisQuery struct {
	Table      string
	Filters    []Filter
	GroupBy    []string
	Aggregates []Aggregate
	Having     []HavingCond
	OrderBy    []OrderKey
	Limit      int
}

// Filter 是单条过滤条件。
//   - Op ∈ comparisonOps：标量，用 Value。
//   - Op == "IN"：用 Values（≥1）。
//   - Op == "BETWEEN"：用 Values（恰 2）。
//   - Op == "IS NULL" / "IS NOT NULL"：无值。
type Filter struct {
	Field  string
	Op     string
	Value  any
	Values []any
}

// Aggregate 是单个聚合输出列。Fn ∈ aggFns；count 的 Column 可空（=COUNT(*)）；
// As 必填且必须是安全标识符（validIdent，防 alias 注入）。
type Aggregate struct {
	Fn     string
	Column string
	As     string
}

// HavingCond 对某聚合别名过滤（Op ∈ comparisonOps）。
type HavingCond struct {
	Alias string
	Op    string
	Value any
}

// OrderKey 排序键：Key ∈ group_by 字段 / 聚合别名。
type OrderKey struct {
	Key  string
	Desc bool
}

// aggFns 是允许的聚合函数白名单（spec K6：不含 median/percentile/窗口）。
var aggFns = map[string]bool{
	"count": true, "count_distinct": true, "sum": true,
	"avg": true, "min": true, "max": true,
}

// comparisonOps 是标量比较 + HAVING 算子白名单。IN/BETWEEN/IS NULL 在 builder 内单独处理。
var comparisonOps = map[string]bool{
	"=": true, "!=": true, "<": true, "<=": true, ">": true, ">=": true,
}

// identRe 校验别名：小写字母开头 + 小写字母/数字/下划线。防 alias 注入。
var identRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func validIdent(s string) bool { return identRe.MatchString(s) }
