// facets.go 从 AnalysisQuery 派生口径标签（facets），用于记忆跨任务检索软重排。
// 标签集合唯一 + 字典序排序，保证确定性输出。
package schema_protocol

import (
	"fmt"
	"sort"
	"strings"
)

// thresholdOps 是判定"阈值比较"的算子集合（filter/having 均适用）。
var thresholdOps = map[string]bool{
	"<": true, "<=": true, ">": true, ">=": true,
}

// DeriveFacets 从查询结构派生一组口径标签：
//   - agg:<fn>     —— 每个去重聚合函数（小写）
//   - dim:<n>      —— GroupBy 长度
//   - filter:null  —— 存在 IS NULL / IS NOT NULL 过滤
//   - cmp:threshold —— 存在 </<=/>/>=  过滤或 having 条件
//   - shape:<v>    —— 形状标签（优先级短路）
//
// 输出去重 + 按字典序排序，结果具有确定性。
func DeriveFacets(q AnalysisQuery) []string {
	set := make(map[string]struct{})

	// —— agg:<fn> ——
	for _, agg := range q.Aggregates {
		fn := strings.ToLower(strings.ReplaceAll(agg.Fn, " ", ""))
		set[fmt.Sprintf("agg:%s", fn)] = struct{}{}
	}

	// —— dim:<n> ——
	set[fmt.Sprintf("dim:%d", len(q.GroupBy))] = struct{}{}

	// —— filter:null ——
	hasFilterNull := false
	for _, f := range q.Filters {
		opUpper := strings.ToUpper(f.Op)
		if opUpper == "IS NULL" || opUpper == "IS NOT NULL" {
			hasFilterNull = true
			break
		}
	}
	if hasFilterNull {
		set["filter:null"] = struct{}{}
	}

	// —— cmp:threshold ——
	hasCmpThreshold := false
	for _, f := range q.Filters {
		if thresholdOps[f.Op] {
			hasCmpThreshold = true
			break
		}
	}
	if !hasCmpThreshold {
		for _, h := range q.Having {
			if thresholdOps[h.Op] {
				hasCmpThreshold = true
				break
			}
		}
	}
	if hasCmpThreshold {
		set["cmp:threshold"] = struct{}{}
	}

	// —— shape:<v>（优先级短路）——
	// 1. filter:null → sentinel
	// 2. cmp:threshold 或 len(Having)>0 → threshold
	// 3. len(GroupBy)>=2 或 去重聚合函数数>1 → composite
	// 4. 否则 → mean
	var shape string
	switch {
	case hasFilterNull:
		shape = "shape:sentinel"
	case hasCmpThreshold || len(q.Having) > 0:
		shape = "shape:threshold"
	case len(q.GroupBy) >= 2 || distinctAggCount(q) > 1:
		shape = "shape:composite"
	default:
		shape = "shape:mean"
	}
	set[shape] = struct{}{}

	// —— 去重 + 字典序排序 ——
	result := make([]string, 0, len(set))
	for k := range set {
		result = append(result, k)
	}
	sort.Strings(result)
	return result
}

// distinctAggCount 返回查询中去重后的聚合函数数量。
func distinctAggCount(q AnalysisQuery) int {
	seen := make(map[string]struct{})
	for _, agg := range q.Aggregates {
		fn := strings.ToLower(strings.ReplaceAll(agg.Fn, " ", ""))
		seen[fn] = struct{}{}
	}
	return len(seen)
}
