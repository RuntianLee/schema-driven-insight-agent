// profile_builder.go 构造分布画像 SQL（SP1.A）。
// 与 sql_builder.go 同包，复用 parseFilterCond / rejectIfPII / sortedKeys 等私有 helpers。
// 安全边界：标识符（table/column/filter key）全经字段白名单 + 非-PII 校验后才 inline；
// filter value 走 ? 占位符；分位百分比是常量 inline。
package schema_protocol

import (
	"fmt"
	"strings"
)

// BuildProfile 构造分布画像主查询（一行统计 + nearest-rank 分位）。
// 输出列顺序（13 列基础；balance 时末尾多一列 total）：
//
//	tot, distinct_cnt, mn, mx, mean, variance,
//	p10, p25, p50, p75, p90, p95, p99
//	[, total]            -- 仅 role=balance
//
// 注：第一列在 SQL 中 alias 为 `tot`（非 spec §2.1 字面的 `cnt`），是为了在
// 百分位子查询 `(SELECT v FROM ordered WHERE rn = MAX(1, (s.tot * P + 99) / 100))`
// 中以 `s.tot` 形式引用。Task 3 的 scan 按位置绑定，列名不影响契约。
// 百分位 rank = ceil(tot * p)（标准 nearest-rank），用整数运算 (tot*P+99)/100 实现
// ceil（P 为百分数整数），不依赖 SQLite math 扩展；此前 CAST(...AS INTEGER) 是截断
// （floor），N 非整除点时取行偏低一位。
// 数值稳定性：variance = E[v²] - E[v]² 在大 balance 值时可能浮点抵消失精度；
// 真实数据若触发，迁移到 Welford 两遍算法（spec §7.1）。
// SYNC: 列顺序必须与 tools.query_distribution 的 profile scan 严格对应。
func (s *Schema) BuildProfile(q DistQuery) (string, []any, error) {
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

	args, where, err := buildWhereClause(tbl, q)
	if err != nil {
		return "", nil, err
	}

	isBalance := colDef.Role == "balance"

	// M3：空表（count=0）时 MIN/MAX/AVG/percentile/SUM 子查询均返 NULL；用 COALESCE 兜 0
	// 让 scanProfile 不会 NULL→float64 失败。上层据 prof.Count<100 走 INSUFFICIENT_DATA。
	sql := fmt.Sprintf(
		"WITH base AS (SELECT %s AS v FROM %s%s), "+
			"stats AS (SELECT COUNT(*) AS tot, COUNT(DISTINCT v) AS distinct_cnt, "+
			"COALESCE(MIN(v), 0) AS mn, COALESCE(MAX(v), 0) AS mx, COALESCE(AVG(v), 0) AS mean, "+
			"COALESCE((SUM(v*v)*1.0/NULLIF(COUNT(*),0) - AVG(v)*AVG(v)), 0) AS variance FROM base), "+
			"ordered AS (SELECT v, ROW_NUMBER() OVER (ORDER BY v) AS rn FROM base) "+
			"SELECT s.tot, s.distinct_cnt, s.mn, s.mx, s.mean, s.variance, "+
			"COALESCE((SELECT v FROM ordered WHERE rn = MAX(1, (s.tot * 10 + 99) / 100)), 0) AS p10, "+
			"COALESCE((SELECT v FROM ordered WHERE rn = MAX(1, (s.tot * 25 + 99) / 100)), 0) AS p25, "+
			"COALESCE((SELECT v FROM ordered WHERE rn = MAX(1, (s.tot * 50 + 99) / 100)), 0) AS p50, "+
			"COALESCE((SELECT v FROM ordered WHERE rn = MAX(1, (s.tot * 75 + 99) / 100)), 0) AS p75, "+
			"COALESCE((SELECT v FROM ordered WHERE rn = MAX(1, (s.tot * 90 + 99) / 100)), 0) AS p90, "+
			"COALESCE((SELECT v FROM ordered WHERE rn = MAX(1, (s.tot * 95 + 99) / 100)), 0) AS p95, "+
			"COALESCE((SELECT v FROM ordered WHERE rn = MAX(1, (s.tot * 99 + 99) / 100)), 0) AS p99",
		q.Column, q.Table, where,
	)
	if isBalance {
		sql += ", COALESCE((SELECT SUM(v) FROM base), 0) AS total"
	}
	sql += " FROM stats s"
	return sql, args, nil
}

// BuildTopN 构造 Top-N 辅助查询（按 player_count 降序，同值按列升序确定性 tie-break）。
// 输出 2 列：value, n（player_count）。
func (s *Schema) BuildTopN(q DistQuery, n int) (string, []any, error) {
	if n <= 0 {
		return "", nil, &SchemaError{Path: "top_n", Hint: fmt.Sprintf("Top-N limit must be > 0, got %d", n)}
	}
	tbl, ok := s.lookupTable(q.Table)
	if !ok {
		return "", nil, &SchemaError{Path: "tables." + q.Table, Hint: fmt.Sprintf("table %q not in schema", q.Table)}
	}
	colDef, ok := tbl.Fields[q.Column]
	if !ok {
		return "", nil, &SchemaError{Path: q.Table + ".fields." + q.Column, Hint: fmt.Sprintf("column %q not in table %q", q.Column, q.Table)}
	}
	if se := rejectIfPII(colDef, q.Table, q.Column); se != nil {
		return "", nil, se
	}
	args, where, err := buildWhereClause(tbl, q)
	if err != nil {
		return "", nil, err
	}
	sql := fmt.Sprintf(
		"SELECT CAST(%s AS TEXT) AS value, COUNT(*) AS n FROM %s%s GROUP BY %s ORDER BY n DESC, %s ASC LIMIT %d",
		q.Column, q.Table, where, q.Column, q.Column, n,
	)
	return sql, args, nil
}

// BuildGroupSummary 构造 group_by 模式下「Top-N 组（按各组 player_count 降序）」的辅助 SQL。
// 输出 2 列：grp（CAST AS TEXT）, n（player_count）。
// 调用方据此遍历各组，再为每组单独调 BuildProfile/BuildDistribution。
//
// 组维度（GroupBy[0]）经字段白名单 + 非-PII 校验；单维约束（多维拒绝）同 BuildDistribution。
// topN 必须 > 0（同 BuildTopN）。
func (s *Schema) BuildGroupSummary(q DistQuery, topN int) (string, []any, error) {
	if topN <= 0 {
		return "", nil, &SchemaError{Path: "groups_top_n", Hint: fmt.Sprintf("Top-N groups limit must be > 0, got %d", topN)}
	}
	tbl, ok := s.lookupTable(q.Table)
	if !ok {
		return "", nil, &SchemaError{Path: "tables." + q.Table, Hint: fmt.Sprintf("table %q not in schema", q.Table)}
	}
	if len(q.GroupBy) != 1 {
		return "", nil, &SchemaError{Path: "group_by", Hint: "BuildGroupSummary 要求单维 group_by"}
	}
	g := q.GroupBy[0]
	gd, ok := tbl.Fields[g]
	if !ok {
		return "", nil, &SchemaError{Path: q.Table + ".fields." + g, Hint: fmt.Sprintf("group_by field %q not in schema", g)}
	}
	if se := rejectIfPII(gd, q.Table, g); se != nil {
		return "", nil, se
	}
	args, where, err := buildWhereClause(tbl, q)
	if err != nil {
		return "", nil, err
	}
	sql := fmt.Sprintf(
		"SELECT CAST(%s AS TEXT) AS grp, COUNT(*) AS n FROM %s%s GROUP BY %s ORDER BY n DESC, %s ASC LIMIT %d",
		g, q.Table, where, g, g, topN,
	)
	return sql, args, nil
}

// BuildGroupTotals 返回总组数与总玩家数（两列一行），用于 GroupsTail 计算。
// SYNC: 列顺序 (total_groups, total_players) 严格对应 tools.runGroupProfile 的 scan。
func (s *Schema) BuildGroupTotals(q DistQuery) (string, []any, error) {
	tbl, ok := s.lookupTable(q.Table)
	if !ok {
		return "", nil, &SchemaError{Path: "tables." + q.Table, Hint: fmt.Sprintf("table %q not in schema", q.Table)}
	}
	if len(q.GroupBy) != 1 {
		return "", nil, &SchemaError{Path: "group_by", Hint: "BuildGroupTotals 要求单维 group_by"}
	}
	g := q.GroupBy[0]
	gd, ok := tbl.Fields[g]
	if !ok {
		return "", nil, &SchemaError{Path: q.Table + ".fields." + g, Hint: fmt.Sprintf("group_by field %q not in schema", g)}
	}
	if se := rejectIfPII(gd, q.Table, g); se != nil {
		return "", nil, se
	}
	args, where, err := buildWhereClause(tbl, q)
	if err != nil {
		return "", nil, err
	}
	sql := fmt.Sprintf("SELECT COUNT(DISTINCT %s) AS total_groups, COUNT(*) AS total_players FROM %s%s", g, q.Table, where)
	return sql, args, nil
}

// buildWhereClause 共享给 BuildDistribution / BuildProfile / BuildTopN / BuildGroupSummary / BuildGroupTotals。
// 复用 sql_builder.go 的字段白名单 + PII 拒绝 + filter value 多态（含区间下钻：同字段数组形）。
// 返回 (args, where 子句含前导空格，err)；无 filter 时 where 是空串。
func buildWhereClause(tbl Table, q DistQuery) ([]any, string, error) {
	var args []any
	var whereParts []string
	for _, k := range sortedKeys(q.Filter) {
		fd, ok := tbl.Fields[k]
		if !ok {
			return nil, "", &SchemaError{Path: q.Table + ".fields." + k, Hint: fmt.Sprintf("filter field %q not in schema", k)}
		}
		if se := rejectIfPII(fd, q.Table, k); se != nil {
			return nil, "", se
		}
		conds, ferr := parseFilterConds(k, q.Filter[k])
		if ferr != nil {
			return nil, "", ferr
		}
		for _, c := range conds {
			whereParts = append(whereParts, k+" "+c.Op+" ?")
			args = append(args, c.Value)
		}
	}
	if len(whereParts) == 0 {
		return args, "", nil
	}
	return args, " WHERE " + strings.Join(whereParts, " AND "), nil
}
