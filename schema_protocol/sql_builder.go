package schema_protocol

import (
	"fmt"
	"sort"
	"strings"
)

// DistQuery 是 query_distribution tool 的内部查询请求。
type DistQuery struct {
	Table     string
	Column    string
	Filter    map[string]any
	BucketKey string
	GroupBy   []string
}

// SchemaError：输入校验失败。Path 指向 yaml 内位置，Hint 给 Agent 自修正提示。
type SchemaError struct {
	Path string
	Hint string
}

func (e *SchemaError) Error() string { return e.Hint }

// BuildDistribution 构造参数化分布 SQL（design-v3 §7；V1 扩展见 architecture-v1 §3）。
// 安全边界：filter value 走 ? 占位符；table/column/group_by 经字段白名单 + 非-PII 校验后才 inline；
// bucket 边界来自 YAML（可信源）inline 进 CASE。任一校验失败 → *SchemaError。
//
// 两种分布模式：
//   - 分桶（BucketKey 非空）：按 glossary 桶的 CASE 分组，ord = 财富索引。
//   - 原始值（BucketKey 空）：按列原始值分组，每个值一行，ord = 列值本身。
//
// 列输出按 column role 条件化（design-v3 §9 的 V1 条件化）：
//   - 始终：[grp,] bucket, player_count, pct_players, cum_pct_players
//   - 仅 role=balance：额外 pct_value, total_value, avg_value, cum_pct_value
//
// cum_* 语义：「该值（桶）及更高」向下累计（ORDER BY ord DESC）。
// 单维 group_by 时多出 grp 首列，pct/cum 为组内占比（PARTITION BY grp）。
func (s *Schema) BuildDistribution(q DistQuery) (string, []any, error) {
	tbl, ok := s.lookupTable(q.Table)
	if !ok {
		return "", nil, &SchemaError{Path: "tables." + q.Table, Hint: fmt.Sprintf("table %q not in schema", q.Table)}
	}
	colDef, ok := tbl.Fields[q.Column]
	if !ok {
		return "", nil, &SchemaError{Path: q.Table + ".fields." + q.Column, Hint: fmt.Sprintf("column %q not in table %q; available: %s", q.Column, q.Table, fieldNames(tbl))}
	}
	if se := rejectIfPII(colDef, q.Table, q.Column); se != nil {
		return "", nil, se
	}

	// 分桶模式需 bucket_key 在 glossary；原始值模式（BucketKey 空）跳过。
	bucketed := q.BucketKey != ""
	var buckets []BucketDef
	if bucketed {
		bs, ok := s.Glossary.Buckets[q.BucketKey]
		if !ok {
			return "", nil, &SchemaError{Path: "glossary.buckets." + q.BucketKey, Hint: fmt.Sprintf("bucket_key %q not in glossary", q.BucketKey)}
		}
		buckets = bs
	}

	// filter：字段白名单 + 非-PII + 值占位符——委托 profile_builder 的共享 buildWhereClause。
	args, where, werr := buildWhereClause(tbl, q)
	if werr != nil {
		return "", nil, werr
	}

	// group_by：字段白名单 + 非-PII；单维支持（组内 PARTITION BY 窗口），多维拒绝。
	for _, g := range q.GroupBy {
		gd, ok := tbl.Fields[g]
		if !ok {
			return "", nil, &SchemaError{Path: q.Table + ".fields." + g, Hint: fmt.Sprintf("group_by field %q not in schema", g)}
		}
		if se := rejectIfPII(gd, q.Table, g); se != nil {
			return "", nil, se
		}
	}
	if len(q.GroupBy) > 1 {
		return "", nil, &SchemaError{Path: "group_by", Hint: "V1 仅支持单维 group_by；多维交叉表推后"}
	}

	// bucket / ord 表达式：分桶用 CASE（label + 财富索引）；原始值用列本身（CAST 文本作 label）。
	var bucketExpr, ordExpr string
	if bucketed {
		bucketExpr = buildCase(q.Column, buckets)
		ordExpr = buildWealthCase(q.Column, buckets)
	} else {
		bucketExpr = "CAST(" + q.Column + " AS TEXT)"
		ordExpr = q.Column
	}

	isBalance := colDef.Role == "balance"
	grouped := len(q.GroupBy) == 1

	// 窗口分区：分组时按 grp 分区。
	totalOver := "OVER ()"
	cumOver := "OVER (ORDER BY ord DESC)"
	if grouped {
		totalOver = "OVER (PARTITION BY grp)"
		cumOver = "OVER (PARTITION BY grp ORDER BY ord DESC)"
	}

	// SYNC: 列集与顺序与 tools.query_distribution.Run 的 scan 严格对应——
	//   [grp,] bucket, player_count, pct_players, [pct_value, total_value, avg_value(仅 balance),] cum_pct_players, [cum_pct_value(仅 balance)]
	// 改动列集/顺序须同步两侧。
	var aggCols, groupCols, outCols []string
	if grouped {
		aggCols = append(aggCols, q.GroupBy[0]+" AS grp")
		groupCols = append(groupCols, "grp")
		outCols = append(outCols, "grp")
	}
	aggCols = append(aggCols, bucketExpr+" AS bucket", ordExpr+" AS ord", "COUNT(*) AS player_count")
	groupCols = append(groupCols, "bucket", "ord")
	if isBalance {
		aggCols = append(aggCols, "SUM("+q.Column+") AS total_value")
	}

	outCols = append(outCols, "bucket", "player_count",
		fmt.Sprintf("ROUND(player_count * 1.0 / SUM(player_count) %s, 4) AS pct_players", totalOver))
	if isBalance {
		outCols = append(outCols,
			fmt.Sprintf("ROUND(total_value * 1.0 / SUM(total_value) %s, 4) AS pct_value", totalOver),
			"total_value",
			"ROUND(total_value * 1.0 / player_count, 2) AS avg_value")
	}
	outCols = append(outCols,
		fmt.Sprintf("ROUND(SUM(player_count) %s * 1.0 / SUM(player_count) %s, 4) AS cum_pct_players", cumOver, totalOver))
	if isBalance {
		outCols = append(outCols,
			fmt.Sprintf("ROUND(SUM(total_value) %s * 1.0 / SUM(total_value) %s, 4) AS cum_pct_value", cumOver, totalOver))
	}

	orderBy := "ORDER BY ord"
	if grouped {
		orderBy = "ORDER BY grp, ord"
	}

	sql := fmt.Sprintf("WITH agg AS (SELECT %s FROM %s%s GROUP BY %s) SELECT %s FROM agg %s",
		strings.Join(aggCols, ", "), q.Table, where, strings.Join(groupCols, ", "),
		strings.Join(outCols, ", "), orderBy)

	return sql, args, nil
}

// buildWealthCase 生成 CASE 表达式，将列值映射为 0..n-1 的整数财富索引（财富升序）。
// 与 buildCase 并行：buckets 已按财富升序排列（YAML parser 保证）。
// 前置条件：col 必须已通过 BuildDistribution 的字段白名单校验（安全 inline）。
func buildWealthCase(col string, buckets []BucketDef) string {
	var sb strings.Builder
	sb.WriteString("CASE")
	for i, b := range buckets {
		if i == len(buckets)-1 {
			fmt.Fprintf(&sb, " ELSE %d", i)
		} else {
			fmt.Fprintf(&sb, " WHEN %s <= %d THEN %d", col, b.Max, i)
		}
	}
	sb.WriteString(" END")
	return sb.String()
}

// 前置条件：col 必须已通过 BuildDistribution 的字段白名单校验（安全 inline）。
func buildCase(col string, buckets []BucketDef) string {
	var sb strings.Builder
	sb.WriteString("CASE")
	for i, b := range buckets {
		if i == len(buckets)-1 {
			fmt.Fprintf(&sb, " ELSE '%s'", b.Label)
		} else {
			fmt.Fprintf(&sb, " WHEN %s <= %d THEN '%s'", col, b.Max, b.Label)
		}
	}
	sb.WriteString(" END")
	return sb.String()
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func fieldNames(t Table) string {
	names := make([]string, 0, len(t.Fields))
	for n := range t.Fields {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// rejectIfPII 拒绝把 PII / 未物化进 Layer2 的列用于查询（pii || omit_in_layer2）。
// 与 ETL basicsColumns、Digest formatFields 同一规则：查询面 = 物化面 = 暴露面。
// 这些列不进 Layer2，查询会以 "no such column" 失败；这里前置拒绝，给 Agent 清晰提示。
func rejectIfPII(fd FieldDef, table, field string) *SchemaError {
	if fd.PII || fd.OmitInLayer2 {
		return &SchemaError{Path: table + ".fields." + field, Hint: fmt.Sprintf("field %q 是 PII / 未物化进 Layer2，不可查询", field)}
	}
	return nil
}

// filterOps 是允许的比较运算符白名单（杜绝 op 注入）。
var filterOps = map[string]bool{"=": true, "<": true, "<=": true, ">": true, ">=": true, "!=": true}

// filterCond 是单字段的一条比较条件（op + value）。多条同字段条件 AND 拼接。
type filterCond struct {
	Op    string
	Value any
}

// parseFilterConds 解析多态 filter 值，返回 1..N 条 AND 条件：
//   - 标量（string/number/...）→ [{"=", v}]，向后兼容 V0 wedge
//   - map{"op":..,"value":..} → [{op, value}]，单条比较
//   - []any 元素均为 map{"op","value"} → [{op,v},...]，**区间下钻用**（如 level [{>=,15},{<=,40}]）
//
// 返回的 op 经白名单校验后才拼入 SQL；value 始终走 ? 占位符绑定；null value 前置拒绝。
func parseFilterConds(field string, raw any) ([]filterCond, error) {
	switch v := raw.(type) {
	case []any:
		if len(v) == 0 {
			return nil, &SchemaError{Path: "filter." + field, Hint: fmt.Sprintf("filter %q array must have ≥1 condition", field)}
		}
		out := make([]filterCond, 0, len(v))
		for i, elem := range v {
			c, err := parseSingleCond(field, elem, i)
			if err != nil {
				return nil, err
			}
			out = append(out, c)
		}
		return out, nil
	default:
		c, err := parseSingleCond(field, raw, -1)
		if err != nil {
			return nil, err
		}
		return []filterCond{c}, nil
	}
}

// parseSingleCond 解析一条 filter 条件（标量等值 / {op,value} 对象）。
// idx ≥ 0 时表示来自数组的第 idx 个元素（错误路径会带上 [idx]）；标量入口下传 -1。
// 数组元素**必须**是 {op,value} 对象——标量数组拒绝，避免歧义。
func parseSingleCond(field string, raw any, idx int) (filterCond, error) {
	path := "filter." + field
	if idx >= 0 {
		path = fmt.Sprintf("filter.%s[%d]", field, idx)
	}
	if raw == nil {
		return filterCond{}, &SchemaError{Path: path, Hint: fmt.Sprintf("filter %q value must not be null", field)}
	}
	m, ok := raw.(map[string]any)
	if !ok {
		if idx >= 0 {
			// 数组元素必须是对象（避免与单值数组语义混淆）
			return filterCond{}, &SchemaError{Path: path, Hint: fmt.Sprintf("filter %q array element must be {\"op\",\"value\"} object", field)}
		}
		return filterCond{Op: "=", Value: raw}, nil
	}
	opRaw, hasOp := m["op"]
	valRaw, hasVal := m["value"]
	if !hasOp || !hasVal {
		return filterCond{}, &SchemaError{Path: path, Hint: fmt.Sprintf("filter %q object must have both \"op\" and \"value\"", field)}
	}
	opStr, ok := opRaw.(string)
	if !ok || !filterOps[opStr] {
		return filterCond{}, &SchemaError{Path: path, Hint: fmt.Sprintf("filter %q op must be one of =,<,<=,>,>=,!=", field)}
	}
	if valRaw == nil {
		return filterCond{}, &SchemaError{Path: path, Hint: fmt.Sprintf("filter %q value must not be null", field)}
	}
	return filterCond{Op: opStr, Value: valRaw}, nil
}
