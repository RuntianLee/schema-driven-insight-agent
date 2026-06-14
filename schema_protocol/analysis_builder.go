// analysis_builder.go 构造 analyze 工具的参数化通用查询 SQL（spec B4）。
// 与 sql_builder.go / profile_builder.go 同包，复用 lookupTable / rejectIfPII / fieldNames。
// 安全边界：标识符（table/列/聚合列/group_by/order 键）全经字段白名单 + 非-PII；
// 别名经 validIdent；算子/函数经白名单；filter/having 值走 ? 占位符。
// 占位符顺序 = WHERE → HAVING（与 SQL 文本出现顺序一致）。
package schema_protocol

import (
	"fmt"
	"strings"
)

// maxAnalysisLimit 是 analyze 结果行数硬上限（护栏，防 group_by 基数爆炸撑爆 payload）。
const maxAnalysisLimit = 1000

// BuildAnalysis 构造扁平声明式查询 SQL。任一校验失败 → *SchemaError。
func (s *Schema) BuildAnalysis(q AnalysisQuery) (string, []any, error) {
	tbl, ok := s.lookupTable(q.Table)
	if !ok {
		return "", nil, &SchemaError{Path: "tables." + q.Table, Hint: fmt.Sprintf("table %q not in schema", q.Table)}
	}
	if len(q.GroupBy) == 0 && len(q.Aggregates) == 0 {
		return "", nil, &SchemaError{Path: "analyze", Hint: "至少需要一个 group_by 或 aggregate 输出列"}
	}

	for _, g := range q.GroupBy {
		gd, ok := tbl.Fields[g]
		if !ok {
			return "", nil, &SchemaError{Path: q.Table + ".fields." + g, Hint: fmt.Sprintf("group_by field %q not in table %q; available: %s", g, q.Table, fieldNames(tbl))}
		}
		if se := rejectIfPII(gd, q.Table, g); se != nil {
			return "", nil, se
		}
	}

	outNames := make(map[string]bool, len(q.GroupBy)+len(q.Aggregates))
	for _, g := range q.GroupBy {
		outNames[g] = true
	}

	selExprs := make([]string, 0, len(q.GroupBy)+len(q.Aggregates))
	selExprs = append(selExprs, q.GroupBy...)
	for _, a := range q.Aggregates {
		expr, err := buildAggExpr(tbl, q.Table, a)
		if err != nil {
			return "", nil, err
		}
		if outNames[a.As] {
			return "", nil, &SchemaError{Path: "aggregates.as", Hint: fmt.Sprintf("duplicate output name %q", a.As)}
		}
		outNames[a.As] = true
		selExprs = append(selExprs, expr)
	}

	args, where, err := buildAnalysisWhere(tbl, q.Table, q.Filters)
	if err != nil {
		return "", nil, err
	}

	var sb strings.Builder
	sb.WriteString("SELECT ")
	sb.WriteString(strings.Join(selExprs, ", "))
	sb.WriteString(" FROM ")
	sb.WriteString(q.Table)
	sb.WriteString(where)
	if len(q.GroupBy) > 0 {
		sb.WriteString(" GROUP BY ")
		sb.WriteString(strings.Join(q.GroupBy, ", "))
	}

	havingSQL, havingArgs, err := buildHaving(q.Having, outNames)
	if err != nil {
		return "", nil, err
	}
	sb.WriteString(havingSQL)
	args = append(args, havingArgs...)

	orderSQL, err := buildOrderBy(q, outNames)
	if err != nil {
		return "", nil, err
	}
	sb.WriteString(orderSQL)

	limit := q.Limit
	if limit <= 0 || limit > maxAnalysisLimit {
		limit = maxAnalysisLimit
	}
	fmt.Fprintf(&sb, " LIMIT %d", limit)

	return sb.String(), args, nil
}

// buildAggExpr 构造单个聚合 SELECT 表达式（含别名）。
func buildAggExpr(tbl Table, table string, a Aggregate) (string, error) {
	if !aggFns[a.Fn] {
		return "", &SchemaError{Path: "aggregates.fn", Hint: fmt.Sprintf("aggregate fn %q not allowed; one of count,count_distinct,sum,avg,min,max", a.Fn)}
	}
	if !validIdent(a.As) {
		return "", &SchemaError{Path: "aggregates.as", Hint: fmt.Sprintf("aggregate alias %q invalid; must match [a-z][a-z0-9_]*", a.As)}
	}
	if a.Fn == "count" && (a.Column == "" || a.Column == "*") {
		return "COUNT(*) AS " + a.As, nil
	}
	fd, ok := tbl.Fields[a.Column]
	if !ok {
		return "", &SchemaError{Path: table + ".fields." + a.Column, Hint: fmt.Sprintf("aggregate column %q not in table %q; available: %s", a.Column, table, fieldNames(tbl))}
	}
	if se := rejectIfPII(fd, table, a.Column); se != nil {
		return "", se
	}
	switch a.Fn {
	case "count":
		return "COUNT(" + a.Column + ") AS " + a.As, nil
	case "count_distinct":
		return "COUNT(DISTINCT " + a.Column + ") AS " + a.As, nil
	case "sum":
		return "SUM(" + a.Column + ") AS " + a.As, nil
	case "avg":
		return "ROUND(AVG(" + a.Column + "), 4) AS " + a.As, nil
	case "min":
		return "MIN(" + a.Column + ") AS " + a.As, nil
	case "max":
		return "MAX(" + a.Column + ") AS " + a.As, nil
	}
	return "", &SchemaError{Path: "aggregates.fn", Hint: "unreachable"}
}

// buildAnalysisWhere 构造参数化 WHERE（含前导空格）；无 filter 时返回空串。
func buildAnalysisWhere(tbl Table, table string, filters []Filter) ([]any, string, error) {
	var args []any
	var parts []string
	for i, f := range filters {
		fd, ok := tbl.Fields[f.Field]
		if !ok {
			return nil, "", &SchemaError{Path: fmt.Sprintf("filters[%d]", i), Hint: fmt.Sprintf("filter field %q not in table %q", f.Field, table)}
		}
		if se := rejectIfPII(fd, table, f.Field); se != nil {
			return nil, "", se
		}
		switch {
		case comparisonOps[f.Op]:
			if f.Value == nil {
				return nil, "", &SchemaError{Path: fmt.Sprintf("filters[%d]", i), Hint: fmt.Sprintf("filter %q op %q requires non-null value", f.Field, f.Op)}
			}
			parts = append(parts, f.Field+" "+f.Op+" ?")
			args = append(args, f.Value)
		case f.Op == "IN":
			if len(f.Values) == 0 {
				return nil, "", &SchemaError{Path: fmt.Sprintf("filters[%d]", i), Hint: fmt.Sprintf("filter %q IN requires >=1 values", f.Field)}
			}
			ph := make([]string, len(f.Values))
			for j := range f.Values {
				if f.Values[j] == nil {
					return nil, "", &SchemaError{Path: fmt.Sprintf("filters[%d]", i), Hint: fmt.Sprintf("filter %q IN value must not be null", f.Field)}
				}
				ph[j] = "?"
				args = append(args, f.Values[j])
			}
			parts = append(parts, f.Field+" IN ("+strings.Join(ph, ", ")+")")
		case f.Op == "BETWEEN":
			if len(f.Values) != 2 || f.Values[0] == nil || f.Values[1] == nil {
				return nil, "", &SchemaError{Path: fmt.Sprintf("filters[%d]", i), Hint: fmt.Sprintf("filter %q BETWEEN requires exactly 2 non-null values", f.Field)}
			}
			parts = append(parts, f.Field+" BETWEEN ? AND ?")
			args = append(args, f.Values[0], f.Values[1])
		case f.Op == "IS NULL":
			parts = append(parts, f.Field+" IS NULL")
		case f.Op == "IS NOT NULL":
			parts = append(parts, f.Field+" IS NOT NULL")
		default:
			return nil, "", &SchemaError{Path: fmt.Sprintf("filters[%d]", i), Hint: fmt.Sprintf("filter %q op %q not allowed", f.Field, f.Op)}
		}
	}
	if len(parts) == 0 {
		return args, "", nil
	}
	return args, " WHERE " + strings.Join(parts, " AND "), nil
}

// buildHaving 构造 HAVING（对输出别名；值占位符）；无条件时返回空串。
func buildHaving(conds []HavingCond, outNames map[string]bool) (string, []any, error) {
	if len(conds) == 0 {
		return "", nil, nil
	}
	var args []any
	var parts []string
	for i, h := range conds {
		if !outNames[h.Alias] {
			return "", nil, &SchemaError{Path: fmt.Sprintf("having[%d]", i), Hint: fmt.Sprintf("having alias %q is not a declared output (group_by field or aggregate alias)", h.Alias)}
		}
		if !comparisonOps[h.Op] {
			return "", nil, &SchemaError{Path: fmt.Sprintf("having[%d]", i), Hint: fmt.Sprintf("having op %q not allowed", h.Op)}
		}
		if h.Value == nil {
			return "", nil, &SchemaError{Path: fmt.Sprintf("having[%d]", i), Hint: fmt.Sprintf("having %q requires non-null value", h.Alias)}
		}
		parts = append(parts, h.Alias+" "+h.Op+" ?")
		args = append(args, h.Value)
	}
	return " HAVING " + strings.Join(parts, " AND "), args, nil
}

// buildOrderBy 构造 ORDER BY + 确定性 tiebreak（追加所有未排序的 group_by 字段 ASC）。
// 无 group_by（纯聚合）时结果天然单行，确定。无任何排序键时返回空串。
func buildOrderBy(q AnalysisQuery, outNames map[string]bool) (string, error) {
	var keys []string
	seen := map[string]bool{}
	for i, o := range q.OrderBy {
		if !outNames[o.Key] {
			return "", &SchemaError{Path: fmt.Sprintf("order_by[%d]", i), Hint: fmt.Sprintf("order_by key %q must be a group_by field or aggregate alias", o.Key)}
		}
		dir := "ASC"
		if o.Desc {
			dir = "DESC"
		}
		keys = append(keys, o.Key+" "+dir)
		seen[o.Key] = true
	}
	for _, g := range q.GroupBy {
		if !seen[g] {
			keys = append(keys, g+" ASC")
			seen[g] = true
		}
	}
	if len(keys) == 0 {
		return "", nil
	}
	return " ORDER BY " + strings.Join(keys, ", "), nil
}
