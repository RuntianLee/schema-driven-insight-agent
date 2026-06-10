package tools

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strconv"

	"github.com/RuntianLee/schema-driven-insight-agent/contract"
	"github.com/RuntianLee/schema-driven-insight-agent/schema_protocol"
)

const (
	// SP1.A framework 默认阈值（schema.yaml 的 tuning: 节可覆盖；零值/未设置时用本组默认）。
	defaultRowsAttachThreshold = 1000 // Task 4 闸门：distinct ≤ 此值才附 Data 逐值行。
	defaultValueTopN           = 10   // DistProfile.TopN 默认长度（值维度）。
	defaultGroupsTopN          = 20   // Task 6 闸门：group_by 时返回最多 N 组，其余进 GroupsTail。
)

// QueryDistributionInput 是 V0 唯一 tool 的输入（design-v3 §9）。
type QueryDistributionInput struct {
	Table     string         `json:"table"`
	Column    string         `json:"column"`
	Filter    map[string]any `json:"filter"`
	BucketKey string         `json:"bucket_key"`
	GroupBy   []string       `json:"group_by"`
}

type DistributionTool struct {
	schema *schema_protocol.Schema
	db     *sql.DB
}

func NewDistributionTool(s *schema_protocol.Schema, db *sql.DB) *DistributionTool {
	return &DistributionTool{schema: s, db: db}
}

// rowsAttachThreshold 返回当前生效的「逐值行附带阈值」：
// schema.Tuning.RowsAttachThreshold > 0 时用 schema 设置，否则回退 framework 默认。
func (t *DistributionTool) rowsAttachThreshold() int64 {
	if t.schema != nil && t.schema.Tuning.RowsAttachThreshold > 0 {
		return int64(t.schema.Tuning.RowsAttachThreshold)
	}
	return defaultRowsAttachThreshold
}

// valueTopN 返回 DistProfile.TopN 长度（值维度）。
func (t *DistributionTool) valueTopN() int {
	if t.schema != nil && t.schema.Tuning.ValueTopN > 0 {
		return t.schema.Tuning.ValueTopN
	}
	return defaultValueTopN
}

// groupsTopN 返回 group_by 模式下返回的组数上限。
func (t *DistributionTool) groupsTopN() int {
	if t.schema != nil && t.schema.Tuning.GroupsTopN > 0 {
		return t.schema.Tuning.GroupsTopN
	}
	return defaultGroupsTopN
}

// perGroupRowsAttachThreshold 返回 group_by 模式下「每组」Data 行附带阈值。
// 显式 tuning > 0 时用 tuning；否则按 rowsAttachThreshold()/groupsTopN() 推导，
// 把 N 组累计行数控制在与非分组同一量级（防 X1 彩排观察到的 ~750KB payload 撑爆 upstream proxy）。
// 推导下限 1：极端 groupsTopN 大、rowsAttachThreshold 小时不要算到 0（0 == 永远不附带）。
func (t *DistributionTool) perGroupRowsAttachThreshold() int64 {
	if t.schema != nil && t.schema.Tuning.PerGroupRowsAttachThreshold > 0 {
		return int64(t.schema.Tuning.PerGroupRowsAttachThreshold)
	}
	n := int64(t.groupsTopN())
	if n <= 0 {
		return t.rowsAttachThreshold()
	}
	derived := t.rowsAttachThreshold() / n
	if derived < 1 {
		derived = 1
	}
	return derived
}

// Run 不返回 error：四状态已覆盖全部失败语义（design-v3 §10）。
// 非 group_by：始终输出 Profile + Top-N + Tail；Data 按阈值附带（Task 4）。
// group_by 单维：返回 Groups（Top-N 组各自画像 + 可选 Data）+ GroupsTail。
func (t *DistributionTool) Run(ctx context.Context, in QueryDistributionInput) contract.Response {
	dq := schema_protocol.DistQuery{
		Table: in.Table, Column: in.Column, Filter: in.Filter,
		BucketKey: in.BucketKey, GroupBy: in.GroupBy,
	}
	if len(in.GroupBy) == 1 {
		return t.runGroupProfile(ctx, dq, in)
	}
	return t.runNonGroupProfile(ctx, dq, in)
}

// runNonGroupProfile：非 group_by 时计算 profile（始终）+ 既有 Data（Task 4 加阈值）。
func (t *DistributionTool) runNonGroupProfile(ctx context.Context, dq schema_protocol.DistQuery, in QueryDistributionInput) contract.Response {
	// isBalance：与 BuildProfile 内部判定同源，决定 scanProfile 是否多扫 total 列。
	isBalance := false
	if fd, ferr := t.schema.ResolveColumn(in.Table, in.Column); ferr == nil {
		isBalance = fd.Role == "balance"
	}
	// 1. Profile 主查询
	psql, pargs, perr := t.schema.BuildProfile(dq)
	if perr != nil {
		return schemaErrResponse(perr)
	}
	prof, scanErr := t.scanProfile(ctx, psql, pargs, isBalance)
	if scanErr != nil {
		return contract.Response{Status: contract.StatusSchemaError, Detail: map[string]any{"profile_sql_error": scanErr.Error()}}
	}
	if prof.Count < 100 {
		return contract.Response{Status: contract.StatusInsufficient,
			Hint: "样本不足以做分布分析，建议放宽 filter 或换数据源"}
	}

	// 2. Top-N
	tsql, targs, terr := t.schema.BuildTopN(dq, t.valueTopN())
	if terr != nil {
		return schemaErrResponse(terr)
	}
	topN, headSum, scanErr2 := t.scanTopN(ctx, tsql, targs)
	if scanErr2 != nil {
		return contract.Response{Status: contract.StatusSchemaError, Detail: map[string]any{"topn_sql_error": scanErr2.Error()}}
	}
	// 回填 TopN 的 pct（基于总 count），然后挂载 + 计算 tail
	if prof.Count > 0 {
		for i := range topN {
			topN[i].PctPlayers = roundPct(float64(topN[i].PlayerCount) / float64(prof.Count))
		}
	}
	prof.TopN = topN
	prof.TailCount = prof.Count - headSum
	if prof.Count > 0 {
		prof.TailPct = roundPct(float64(prof.TailCount) / float64(prof.Count))
	}

	// 3. Data 行附带：仅当 distinct ≤ rowsAttachThreshold 才查 BuildDistribution；超阈值留空（仅给画像）。
	// 防御性透传非 OK status（含 INSUFFICIENT/SchemaError），防阈值口径漂移后悄悄丢状态。
	var rowData []contract.BucketRow
	if prof.Distinct <= t.rowsAttachThreshold() {
		data := t.runDistribution(ctx, dq, in)
		if data.Status != contract.StatusOK {
			return data
		}
		rowData = data.Data
	}
	return contract.Response{Status: contract.StatusOK, Profile: &prof, Data: rowData}
}

// runDistribution 复用 SP1 既有逐值/分桶/group_by 行 SQL + scan。
// 返回 INSUFFICIENT / OK + Data；上层按需取其 Data 字段。
func (t *DistributionTool) runDistribution(ctx context.Context, dq schema_protocol.DistQuery, in QueryDistributionInput) contract.Response {
	sqlText, args, err := t.schema.BuildDistribution(dq)
	if err != nil {
		return schemaErrResponse(err)
	}
	rows, err := t.db.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return contract.Response{Status: contract.StatusSchemaError, Detail: map[string]any{"sql_error": err.Error()}}
	}
	defer rows.Close()

	grouped := len(in.GroupBy) == 1
	isBalance := false
	if fd, ferr := t.schema.ResolveColumn(in.Table, in.Column); ferr == nil {
		isBalance = fd.Role == "balance"
	}

	var data []contract.BucketRow
	var total int64
	for rows.Next() {
		var b contract.BucketRow
		// SYNC: scan 目标顺序必须与 BuildDistribution 的 SELECT 列集逐列对应——
		//   [grp,] bucket, player_count, pct_players, [pct_value, total_value, avg_value(仅 balance),] cum_pct_players, [cum_pct_value(仅 balance)]
		// 列集随 grouped / isBalance 变化；改任一侧必须同步另一侧，否则 positional scan 静默错位。
		dest := make([]any, 0, 9)
		if grouped {
			dest = append(dest, &b.Group)
		}
		dest = append(dest, &b.Bucket, &b.PlayerCount, &b.PctPlayers)
		if isBalance {
			dest = append(dest, &b.PctValue, &b.TotalValue, &b.AvgValue)
		}
		dest = append(dest, &b.CumPctPlayers)
		if isBalance {
			dest = append(dest, &b.CumPctValue)
		}
		if scanErr := rows.Scan(dest...); scanErr != nil {
			return contract.Response{Status: contract.StatusSchemaError, Detail: map[string]any{"scan_error": scanErr.Error()}}
		}
		data = append(data, b)
		total += b.PlayerCount
	}
	if err := rows.Err(); err != nil {
		return contract.Response{Status: contract.StatusSchemaError, Detail: map[string]any{"rows_error": err.Error()}}
	}
	if total < 100 {
		return contract.Response{Status: contract.StatusInsufficient,
			Hint: "样本不足以做分布分析，建议放宽 filter 或换数据源"}
	}
	return contract.Response{Status: contract.StatusOK, Data: data}
}

func schemaErrResponse(err error) contract.Response {
	var se *schema_protocol.SchemaError
	if errors.As(err, &se) {
		return contract.Response{Status: contract.StatusSchemaError, SchemaPath: se.Path, Hint: se.Hint}
	}
	return contract.Response{Status: contract.StatusSchemaError, Hint: err.Error()}
}

// roundPct 四舍五入到 4 位小数（与 BucketRow.PctPlayers SQL ROUND 同口径）。
func roundPct(x float64) float64 {
	return float64(int64(x*10000+0.5)) / 10000.0
}

// scanProfile 扫描 BuildProfile 一行结果。
// SYNC: 列顺序与 BuildProfile 的 SELECT 严格对应——tot, distinct_cnt, mn, mx, mean, variance, p10..p99 [, total]。
// isBalance 必须与 BuildProfile 调用方的 colDef.Role=="balance" 判定一致（同源），
// 决定是否多扫一列 total。M1：原 substring sniff 已替换为显式参数，杜绝隐式 SQL 耦合。
func (t *DistributionTool) scanProfile(ctx context.Context, sqlText string, args []any, isBalance bool) (contract.DistProfile, error) {
	row := t.db.QueryRowContext(ctx, sqlText, args...)
	var p contract.DistProfile
	var variance float64
	if isBalance {
		var total int64
		if err := row.Scan(&p.Count, &p.Distinct, &p.Min, &p.Max, &p.Mean, &variance,
			&p.P10, &p.P25, &p.Median, &p.P75, &p.P90, &p.P95, &p.P99, &total); err != nil {
			return p, err
		}
		p.Total = &total
	} else {
		if err := row.Scan(&p.Count, &p.Distinct, &p.Min, &p.Max, &p.Mean, &variance,
			&p.P10, &p.P25, &p.Median, &p.P75, &p.P90, &p.P95, &p.P99); err != nil {
			return p, err
		}
	}
	if variance < 0 {
		variance = 0 // E[v²]-E[v]² 浮点噪声可能产生微负，兜底
	}
	p.StdDev = math.Sqrt(variance)
	return p, nil
}

// scanTopN 返回 TopN 行 + 玩家头部合计（用于 TailCount = total - headSum）。
// PctPlayers 不在此填（需要总 count，由调用方回填）。
func (t *DistributionTool) scanTopN(ctx context.Context, sqlText string, args []any) ([]contract.TopRow, int64, error) {
	rows, err := t.db.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []contract.TopRow
	var head int64
	for rows.Next() {
		var r contract.TopRow
		if err := rows.Scan(&r.Value, &r.PlayerCount); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
		head += r.PlayerCount
	}
	return out, head, rows.Err()
}

// runGroupProfile：group_by 单维模式。
// 步骤：① BuildGroupSummary 取 Top-N 组 + 各组 player_count；② 总组数/总玩家数另一查询；
//
//	③ 逐组以 filter += {group_col=value} 调 BuildProfile/BuildTopN + 按阈值附 Data；
//	④ 计算 GroupsTail = (总组 - Top-N 内组数, 尾部 player_count, pct)。
func (t *DistributionTool) runGroupProfile(ctx context.Context, dq schema_protocol.DistQuery, in QueryDistributionInput) contract.Response {
	// ① Top-N 组（按 player_count 降序）
	gsql, gargs, gerr := t.schema.BuildGroupSummary(dq, t.groupsTopN())
	if gerr != nil {
		return schemaErrResponse(gerr)
	}
	type topGroup struct {
		grp string
		n   int64
	}
	groupRows, err := t.db.QueryContext(ctx, gsql, gargs...)
	if err != nil {
		return contract.Response{Status: contract.StatusSchemaError, Detail: map[string]any{"group_sql_error": err.Error()}}
	}
	defer groupRows.Close()
	var topGroups []topGroup
	var headPlayers int64
	for groupRows.Next() {
		var g topGroup
		if scanErr := groupRows.Scan(&g.grp, &g.n); scanErr != nil {
			return contract.Response{Status: contract.StatusSchemaError, Detail: map[string]any{"scan_error": scanErr.Error()}}
		}
		topGroups = append(topGroups, g)
		headPlayers += g.n
	}
	// I1：rows.Err() 检查与其他 scan loop 一致——驱动中途出错（连接掉/数据损坏）不能静默吞掉。
	if err := groupRows.Err(); err != nil {
		return contract.Response{Status: contract.StatusSchemaError, Detail: map[string]any{"group_rows_error": err.Error()}}
	}

	// ② 总组数 & 总玩家数
	totSQL, totArgs, terr := t.schema.BuildGroupTotals(dq)
	if terr != nil {
		return schemaErrResponse(terr)
	}
	var totalGroups, totalPlayers int64
	if err := t.db.QueryRowContext(ctx, totSQL, totArgs...).Scan(&totalGroups, &totalPlayers); err != nil {
		return contract.Response{Status: contract.StatusSchemaError, Detail: map[string]any{"totals_sql_error": err.Error()}}
	}
	if totalPlayers < 100 {
		return contract.Response{Status: contract.StatusInsufficient,
			Hint: "样本不足以做分布分析，建议放宽 filter 或换数据源"}
	}

	// ③ 逐组：profile + 可选 Data
	gcol := in.GroupBy[0]
	// isBalance 在循环外算一次（每组同一 column）。
	isBalance := false
	if fd, ferr := t.schema.ResolveColumn(in.Table, in.Column); ferr == nil {
		isBalance = fd.Role == "balance"
	}
	groups := make([]contract.GroupProfile, 0, len(topGroups))
	for _, tg := range topGroups {
		perGroupQ := withGroupFilter(dq, gcol, tg.grp)
		// 3.1 profile
		psql, pargs, perr := t.schema.BuildProfile(perGroupQ)
		if perr != nil {
			return schemaErrResponse(perr)
		}
		prof, scanErr := t.scanProfile(ctx, psql, pargs, isBalance)
		if scanErr != nil {
			return contract.Response{Status: contract.StatusSchemaError, Detail: map[string]any{"profile_sql_error": scanErr.Error()}}
		}
		// 3.2 Top-N + pct 回填
		tsql, targs, tperr := t.schema.BuildTopN(perGroupQ, t.valueTopN())
		if tperr != nil {
			return schemaErrResponse(tperr)
		}
		topN, headSum, scanErr2 := t.scanTopN(ctx, tsql, targs)
		if scanErr2 != nil {
			return contract.Response{Status: contract.StatusSchemaError, Detail: map[string]any{"topn_sql_error": scanErr2.Error()}}
		}
		if prof.Count > 0 {
			for i := range topN {
				topN[i].PctPlayers = roundPct(float64(topN[i].PlayerCount) / float64(prof.Count))
			}
		}
		prof.TopN = topN
		prof.TailCount = prof.Count - headSum
		if prof.Count > 0 {
			prof.TailPct = roundPct(float64(prof.TailCount) / float64(prof.Count))
		}
		// 3.3 该组 Data：仅 distinct ≤ 「每组阈值」（≠ 非分组阈值！）。
		// F1：group_by 时把每组上限独立出来——默认按 rows_attach / groups_top_n 推导，
		// 避免 N 组各自带数百行累积撑爆 upstream proxy（X1 彩排实测 ~750KB payload）。
		// I2：runDistribution 自带 count<100 → StatusInsufficient 闸（runDistribution 设计如此）。
		// 此处仅透传 SchemaError；StatusInsufficient 故意吞掉——小组保留 Profile（已就绪），
		// Data 留空（与 distinct 超阈值的语义一致：组在 Groups[] 中但无逐值行）。
		// 全局闸门由前面的 totalPlayers<100 兜底保证不会整体退化为 INSUFFICIENT。
		var gData []contract.BucketRow
		if prof.Distinct <= t.perGroupRowsAttachThreshold() {
			data := t.runDistribution(ctx, perGroupQ, QueryDistributionInput{
				Table: perGroupQ.Table, Column: perGroupQ.Column,
				Filter: perGroupQ.Filter, BucketKey: perGroupQ.BucketKey,
			})
			if data.Status == contract.StatusSchemaError {
				return data
			}
			gData = data.Data
		}
		groups = append(groups, contract.GroupProfile{Group: tg.grp, Profile: prof, Data: gData})
	}

	// ④ GroupsTail
	tail := &contract.GroupsTail{
		GroupCount:  totalGroups - int64(len(topGroups)),
		PlayerCount: totalPlayers - headPlayers,
	}
	if totalPlayers > 0 {
		tail.PctPlayers = roundPct(float64(tail.PlayerCount) / float64(totalPlayers))
	}
	if tail.GroupCount == 0 {
		tail = nil // 无尾部时不输出
	}
	return contract.Response{Status: contract.StatusOK, Groups: groups, GroupsTail: tail}
}

// withGroupFilter 把 dq.GroupBy[0]=value 注入 filter（等值），并清空 GroupBy。
// value 是 BuildGroupSummary 返回的 CAST AS TEXT：整型列重 parse 为 int64；其余作字符串。
// I3 假设：SQLite CAST(integer_col AS TEXT) 输出无小数点（如 "1" 而非 "1.0"）——
// 仅当 group 列声明为 INTEGER 时成立。若未来加 REAL 类型 group 列（schema.yaml 无此类），
// "1.0" 会 ParseInt 失败、走字符串 path，WHERE col = "1.0" 匹 0 行 → 静默 INSUFFICIENT。
// 当前所有 group 候选（server_id 等）均为整型，安全；新增 REAL 列前需补类型分发。
func withGroupFilter(dq schema_protocol.DistQuery, gcol, gval string) schema_protocol.DistQuery {
	newFilter := make(map[string]any, len(dq.Filter)+1)
	for k, v := range dq.Filter {
		newFilter[k] = v
	}
	if iv, err := strconv.ParseInt(gval, 10, 64); err == nil {
		newFilter[gcol] = iv
	} else {
		newFilter[gcol] = gval
	}
	return schema_protocol.DistQuery{
		Table: dq.Table, Column: dq.Column, Filter: newFilter,
		BucketKey: dq.BucketKey, GroupBy: nil,
	}
}
