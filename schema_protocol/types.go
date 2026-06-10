// Package schema_protocol 把业务知识（字段 / 桶 / 术语）从代码逻辑抽离为 YAML 协议。
// 叶子包：仅依赖 yaml.v3 + stdlib。design-v3 §4「Schema YAML 协议」、§7「SQL Builder」。
package schema_protocol

type Schema struct {
	Version       int
	Domain        string
	DataSources   map[string]DataSource
	StateTables   map[string]Table
	DerivedTables map[string]Table
	Glossary      Glossary
	Tuning        Tuning // 可选画像/查询阈值（缺省由 tool 侧用 framework 常量回退）
}

// Tuning 承载 query_distribution 的画像/Top-N 阈值。
// 所有字段为 0 时表示「未设置」，调用方应回退到 framework 内置默认。
// SP1.A spec §7.3：允许 adapter 在 schema.yaml 里通过 `tuning:` 顶层节自调。
type Tuning struct {
	// RowsAttachThreshold：当某次分布 distinct 值数 ≤ 此值时，画像之外额外附带逐值 Data 行；
	// 0 表示未设置，工具回退 framework 默认（1000）。
	RowsAttachThreshold int
	// ValueTopN：DistProfile.TopN 长度（值维度 Top-N）；0 表示未设置，回退默认（10）。
	ValueTopN int
	// GroupsTopN：group_by 模式下 Top-N 组数；0 表示未设置，回退默认（20）。
	GroupsTopN int
	// PerGroupRowsAttachThreshold：group_by 模式下，每组单独使用的 Data 行附带阈值；
	// 0 表示未设置，由 framework 推导（rows_attach_threshold / groups_top_n，下限 1）
	// 把总 payload 控制在与非分组同一量级——避免「N 组 × 每组数千行」撑爆上游。
	// 若 adapter 业务上确实需要单组高 distinct + 高分组数，可在 schema.yaml 显式上调。
	PerGroupRowsAttachThreshold int
}

type DataSource struct {
	Type   string
	DSNEnv string
	Access string
	Path   string
}

type Table struct {
	Nature      string // snapshot | append-only | derived
	PrimaryKey  []string
	Fields      map[string]FieldDef
	DerivedFrom string
	Method      string
}

type FieldDef struct {
	Type         string
	Role         string
	PK           bool
	PII          bool
	OmitInLayer2 bool
	CurrencyType string
	GlossaryKey  string
}

type Glossary struct {
	CurrencyTypes map[string]string
	Buckets       map[string][]BucketDef
}

// BucketDef：末桶 Max==0 表示 +∞（YAML 中写 null）；max: 0 是哨兵值，V0 中禁止用于真实零上界。
type BucketDef struct {
	Min   int64
	Max   int64
	Label string
}
