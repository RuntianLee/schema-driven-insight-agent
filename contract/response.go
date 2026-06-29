// Package contract 承载 framework 内多消费者共享的纯数据类型。
// 只依赖 stdlib，不依赖任何 framework 实现包（叶子节点）。
package contract

type Status string

const (
	StatusOK           Status = "OK"
	StatusInsufficient Status = "INSUFFICIENT_DATA"
	StatusDegenerate   Status = "DEGENERATE"
	StatusSchemaError  Status = "SCHEMA_ERROR"
)

// BucketRow 是分布查询单桶/单值结果（V0 + SP1 形状不变）。
// 价值列（PctValue / TotalValue / AvgValue / CumPctValue）仅在 column role=balance 时
// 由 SQL 输出并填充；非 balance 留零值 + omitempty。
// CumPct* 语义：从「该值（桶）及更高」向下累计（SQL 内 ORDER BY ord DESC）——
// 头部效应 / 进度卡点信号。
type BucketRow struct {
	Group         string  `json:"group,omitempty"`
	Bucket        string  `json:"bucket"`
	PlayerCount   int64   `json:"player_count"`
	PctPlayers    float64 `json:"pct_players"`
	PctValue      float64 `json:"pct_value,omitempty"`
	TotalValue    int64   `json:"total_value,omitempty"`
	AvgValue      float64 `json:"avg_value,omitempty"`
	CumPctPlayers float64 `json:"cum_pct_players"`         // 该值及更高的累计玩家占比
	CumPctValue   float64 `json:"cum_pct_value,omitempty"` // 该值及更高的累计货币占比（仅 balance）
}

// TopRow 是 DistProfile.TopN 中的一行：原始值 + 玩家数 + 占比。
// Value 用文本承载，与 BucketRow.Bucket 同口径（CAST AS TEXT）。
type TopRow struct {
	Value       string  `json:"value"`
	PlayerCount int64   `json:"player_count"`
	PctPlayers  float64 `json:"pct_players"`
}

// ColumnMeta 描述 TableResult 的一列。Type 取自 schema 字段（聚合列可空）。
type ColumnMeta struct {
	Name string `json:"name"`
	Type string `json:"type,omitempty"`
}

// TableResult 是通用 analyze 工具的表格结果（V2 路线 B）。
// 与分布特化的 Profile/Groups/Data 并列、互斥使用——agent/Advisor/Memory 统一消费。
type TableResult struct {
	Columns  []ColumnMeta `json:"columns"`
	Rows     [][]any      `json:"rows"`
	RowCount int64        `json:"row_count"`
}

// DistProfile 是分布的紧凑统计描述（针对 WHERE 过滤后的子集）。SP1.A 起始终输出。
// 分位用 nearest-rank（无线性插值）；Total 仅 role=balance 时填（其余列总和无业务意义）。
// TailCount/TailPct = 0 表示 TopN 已覆盖全部 distinct 值（无尾部）。
// TopN 用 omitempty：嵌入零值 DistProfile（如未初始化的 GroupProfile）时不暴露 `null` 给 LLM。
type DistProfile struct {
	Count     int64    `json:"count"`
	Distinct  int64    `json:"distinct"`
	Min       float64  `json:"min"`
	Max       float64  `json:"max"`
	Mean      float64  `json:"mean"`
	Median    float64  `json:"median"`
	P10       float64  `json:"p10"`
	P25       float64  `json:"p25"`
	P75       float64  `json:"p75"`
	P90       float64  `json:"p90"`
	P95       float64  `json:"p95"`
	P99       float64  `json:"p99"`
	StdDev    float64  `json:"stddev"`
	TopN      []TopRow `json:"top_n,omitempty"`
	TailCount int64    `json:"tail_count"`
	TailPct   float64  `json:"tail_pct"`
	Total     *int64   `json:"total,omitempty"`
}

// GroupProfile 是 group_by 模式下单组的画像 + 该组可选逐值行。
type GroupProfile struct {
	Group   string      `json:"group"`
	Profile DistProfile `json:"profile"`
	Data    []BucketRow `json:"data,omitempty"`
}

// GroupsTail 描述 group_by 中 Top-N 组之外的剩余组聚合。
type GroupsTail struct {
	GroupCount  int64   `json:"group_count"`
	PlayerCount int64   `json:"player_count"`
	PctPlayers  float64 `json:"pct_players"`
}

// Response 是所有 tool 的统一返回（design-v3 §10）。四状态覆盖全部失败语义，tool 不返回 error。
// SP1.A 扩展：Profile（非 group_by 时填）/ Groups + GroupsTail（group_by 时填）。
// Data 在非 group_by 且 distinct ≤ 阈值时附带；group_by 时 Data 留空，行进入各 GroupProfile.Data。
type Response struct {
	Status     Status         `json:"status"`
	Profile    *DistProfile   `json:"profile,omitempty"`
	Groups     []GroupProfile `json:"groups,omitempty"`
	GroupsTail *GroupsTail    `json:"groups_tail,omitempty"`
	Data       []BucketRow    `json:"data,omitempty"`
	Table      *TableResult   `json:"table,omitempty"`
	Hint       string         `json:"hint,omitempty"`
	SchemaPath string         `json:"schema_path,omitempty"`
	Detail     map[string]any `json:"detail,omitempty"`
}

// ClaimAnchor 是 Analyst 对单个定量主张的自产接地声明。
// Anchor 语法复用 Phase 1 单元格路径/派生式（evaluators.ResolveAnchor 直接消费）。
type ClaimAnchor struct {
	Claim        string  `json:"claim"`         // 原文定量主张片段
	Anchor       string  `json:"anchor"`        // 路径或派生式；找不到出处留空
	Kind         string  `json:"kind"`          // "cell" | "derived"
	ClaimedValue ClaimedNumber `json:"claimed_value"` // 从主张读到的数值；容错带单位字符串（ClaimedNumber）
}
