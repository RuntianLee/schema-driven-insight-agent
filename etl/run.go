// run.go：schema 驱动的完整 ETL 编排（cmd/etl 的核心）。
// 顺序沿用 td 实证语义：逐个 state 表（任一失败即返回，不写 OK health）
// → 戳 data_as_of → pivot 派生表（末尾写 health，反映全部就绪）。
package etl

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/RuntianLee/schema-driven-insight-agent/schema_protocol"
)

// RunOptions 驱动一次完整 ETL。SQLitePath/HealthPath 为空时从 schema 解析默认值。
type RunOptions struct {
	SchemaPath string
	DSN        string
	SQLitePath string // 空 → data_sources.layer2.path（相对 schema 目录）
	HealthPath string // 空 → etl_policy.health_path（相对 schema 目录）或 db 同目录 etl_health.json
}

type stampSpec struct{ Table, Column string }

// runPlan 是从 schema 推导出的执行装配（纯数据，便于对账单测）。
type runPlan struct {
	Basics     []BasicsOptions
	Stamps     []stampSpec
	Currencies CurrenciesOptions
}

// ResolveOutputs 解析 Layer2 与 health 的落盘路径（cmd/seed 复用同一规则）。
func ResolveOutputs(s *schema_protocol.Schema, schemaPath, sqliteOverride, healthOverride string) (sqlitePath, healthPath string, err error) {
	dir := filepath.Dir(schemaPath)
	sqlitePath = sqliteOverride
	if sqlitePath == "" {
		l2, ok := s.DataSources["layer2"]
		if !ok || l2.Path == "" {
			return "", "", fmt.Errorf("未指定 SQLite 路径：传 -sqlite 或在 schema data_sources.layer2.path 声明")
		}
		sqlitePath = filepath.Join(dir, l2.Path)
	}
	healthPath = healthOverride
	if healthPath == "" {
		if s.ETLPolicy != nil && s.ETLPolicy.HealthPath != "" {
			healthPath = filepath.Join(dir, s.ETLPolicy.HealthPath)
		} else {
			healthPath = filepath.Join(filepath.Dir(sqlitePath), "etl_health.json")
		}
	}
	return sqlitePath, healthPath, nil
}

// ResolveSalt 解析脱敏盐（hash_salt_env 非空时优先且必须有值）。
func ResolveSalt(p *schema_protocol.ETLPolicy) (string, error) {
	if p.HashSaltEnv != "" {
		v := os.Getenv(p.HashSaltEnv)
		if v == "" {
			return "", fmt.Errorf("etl_policy.hash_salt_env=%s 但该环境变量为空", p.HashSaltEnv)
		}
		return v, nil
	}
	if p.HashSalt == "" {
		return "", fmt.Errorf("etl_policy 未提供 hash_salt / hash_salt_env")
	}
	return p.HashSalt, nil
}

// DSNEnvFromSchema 读 schema data_sources 中 type=postgres 源的 dsn_env 名（cmd/etl 兜底用）。
func DSNEnvFromSchema(schemaPath string) (string, error) {
	raw, err := os.ReadFile(schemaPath)
	if err != nil {
		return "", fmt.Errorf("read schema %s: %w", schemaPath, err)
	}
	s, err := schema_protocol.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse schema: %w", err)
	}
	for _, src := range s.DataSources {
		if src.Type == "postgres" && src.DSNEnv != "" {
			return src.DSNEnv, nil
		}
	}
	return "", fmt.Errorf("schema data_sources 无 type=postgres 的 dsn_env 声明")
}

func sortedTableNames(m map[string]schema_protocol.Table) []string {
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

func buildPlan(s *schema_protocol.Schema, o RunOptions) (*runPlan, error) {
	p := s.ETLPolicy
	if p == nil {
		return nil, fmt.Errorf("schema 缺 etl_policy 块（cmd/etl 需要：hash_salt/min_rows 等，见 ADAPTER_GUIDE）")
	}
	sqlitePath, healthPath, err := ResolveOutputs(s, o.SchemaPath, o.SQLitePath, o.HealthPath)
	if err != nil {
		return nil, err
	}
	salt, err := ResolveSalt(p)
	if err != nil {
		return nil, err
	}

	plan := &runPlan{}
	for _, table := range sortedTableNames(s.StateTables) {
		plan.Basics = append(plan.Basics, BasicsOptions{
			PGDSN: o.DSN, SQLitePath: sqlitePath, SchemaPath: o.SchemaPath,
			Table: table, MinRows: p.MinRows, IndexCols: IndexColumnsFor(s, table),
		})
		if col, ok := LastSeenColumn(s, table); ok {
			plan.Stamps = append(plan.Stamps, stampSpec{Table: table, Column: col})
		}
	}

	var pivots []string
	for name, t := range s.DerivedTables {
		if t.Method == "pivot_money_columns" {
			pivots = append(pivots, name)
		}
	}
	if len(pivots) != 1 {
		return nil, fmt.Errorf("v0.2 cmd/etl 需要恰好 1 个 pivot_money_columns 派生表，找到 %d", len(pivots))
	}
	dest := pivots[0]
	src := s.DerivedTables[dest].DerivedFrom
	pidCol, err := ActorIDColumn(s, src)
	if err != nil {
		return nil, err
	}
	money, err := MoneyColumnsFor(s, src)
	if err != nil {
		return nil, err
	}
	cur := CurrenciesOptions{
		PGDSN: o.DSN, SQLitePath: sqlitePath, HealthPath: healthPath,
		SrcTable: src, DestTable: dest, PIDCol: pidCol, Salt: salt,
		MinRows: p.MinRows, SchemaVersion: s.Version, Money: money,
		SchemaPath: o.SchemaPath, Frozen: p.Frozen,
	}
	if p.HealthMinRows > 0 {
		mr := p.HealthMinRows
		cur.MinRowsOverride = &mr
	}
	plan.Currencies = cur
	return plan, nil
}

// RunAll：解析 schema → buildPlan → basics → data_as_of（多 last_seen 表取 MAX，写一次）→ pivot+health。
func RunAll(ctx context.Context, o RunOptions) error {
	raw, err := os.ReadFile(o.SchemaPath)
	if err != nil {
		return fmt.Errorf("read schema %s: %w", o.SchemaPath, err)
	}
	s, err := schema_protocol.Parse(raw)
	if err != nil {
		return fmt.Errorf("parse schema: %w", err)
	}
	plan, err := buildPlan(s, o)
	if err != nil {
		return err
	}
	for _, b := range plan.Basics {
		if err := PullBasics(ctx, b); err != nil {
			return fmt.Errorf("ETL %s: %w", b.Table, err)
		}
	}
	var asOf int64
	for _, st := range plan.Stamps {
		v, err := MaxColumn(plan.Currencies.SQLitePath, st.Table, st.Column)
		if err != nil {
			return fmt.Errorf("data_as_of (%s.%s): %w", st.Table, st.Column, err)
		}
		if v > asOf {
			asOf = v
		}
	}
	if len(plan.Stamps) > 0 {
		if err := WriteDataAsOf(plan.Currencies.SQLitePath, asOf); err != nil {
			return err
		}
	}
	cur := plan.Currencies
	cur.DataAsOf = asOf
	return PullCurrencies(ctx, cur)
}
