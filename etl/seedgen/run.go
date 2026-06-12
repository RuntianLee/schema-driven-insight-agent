package seedgen

import (
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/RuntianLee/schema-driven-insight-agent/etl"
	"github.com/RuntianLee/schema-driven-insight-agent/etl_health"
	"github.com/RuntianLee/schema-driven-insight-agent/schema_protocol"
)

// RunOptions 驱动一次合成 seed。路径解析规则与 cmd/etl 完全一致（etl.ResolveOutputs）。
type RunOptions struct {
	SchemaPath string
	SpecPath   string
	SQLitePath string
	HealthPath string
}

// Run：解析 schema+spec → 逐表生成+LoadBasics → data_as_of → pivot+LoadCurrencies → health。
// 镜像 etl.RunAll 的顺序与 health 语义，dev/demo 路径与真库路径行为一致。
func Run(o RunOptions) error {
	rawSchema, err := os.ReadFile(o.SchemaPath)
	if err != nil {
		return fmt.Errorf("read schema %s: %w", o.SchemaPath, err)
	}
	s, err := schema_protocol.Parse(rawSchema)
	if err != nil {
		return fmt.Errorf("parse schema: %w", err)
	}
	if s.ETLPolicy == nil {
		return fmt.Errorf("schema 缺 etl_policy 块（cmd/seed 需要 hash_salt 等）")
	}
	rawSpec, err := os.ReadFile(o.SpecPath)
	if err != nil {
		return fmt.Errorf("read seed spec %s: %w", o.SpecPath, err)
	}
	sp, err := ParseSpec(rawSpec)
	if err != nil {
		return err
	}
	sqlitePath, healthPath, err := etl.ResolveOutputs(s, o.SchemaPath, o.SQLitePath, o.HealthPath)
	if err != nil {
		return err
	}
	salt, err := etl.ResolveSalt(s.ETLPolicy)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(sqlitePath), 0o755); err != nil {
		return err
	}

	rng := rand.New(rand.NewPCG(uint64(sp.Seed), uint64(sp.Seed)>>1|1))

	// 逐表（排序确定性）：生成全部物化列 → LoadBasics。
	tableNames := make([]string, 0, len(sp.Tables))
	for k := range sp.Tables {
		tableNames = append(tableNames, k)
	}
	sort.Strings(tableNames)

	generated := map[string]map[string][]int64{} // table → col → values
	for _, table := range tableNames {
		tb := sp.Tables[table]
		cols, err := etl.BasicsColumns(s, table)
		if err != nil {
			return err
		}
		var missing []string
		for _, c := range cols {
			if _, ok := tb.Columns[c.Name]; !ok {
				missing = append(missing, c.Name)
			}
		}
		if len(missing) > 0 {
			return fmt.Errorf("seed spec %s: 缺生成器的物化列: %s（每个物化列都必须声明）",
				table, strings.Join(missing, ", "))
		}
		colVals := map[string][]int64{}
		for _, c := range cols { // cols 已字母序 → RNG 消耗顺序确定
			colVals[c.Name] = genColumn(rng, tb.Columns[c.Name], tb.Rows)
		}
		// as_of 锚定：last_seen 列生成值不可超过 as_of，且首行钉 = as_of（MAX 精确）。
		if lastSeen, ok := etl.LastSeenColumn(s, table); ok && sp.AsOf > 0 {
			vals := colVals[lastSeen]
			for _, v := range vals {
				if v > sp.AsOf {
					return fmt.Errorf("seed spec %s.%s: 生成值 %d 超过 as_of %d", table, lastSeen, v, sp.AsOf)
				}
			}
			vals[0] = sp.AsOf
		}
		rows := make([][]any, tb.Rows)
		for i := 0; i < tb.Rows; i++ {
			row := make([]any, len(cols))
			for j, c := range cols {
				row[j] = colVals[c.Name][i]
			}
			rows[i] = row
		}
		if err := etl.LoadBasics(rows, cols, table, sqlitePath, etl.IndexColumnsFor(s, table)); err != nil {
			return fmt.Errorf("load %s: %w", table, err)
		}
		generated[table] = colVals
	}

	// data_as_of：与 RunAll 同语义（多 last_seen 表取 MAX，写一次）。
	var asOf int64
	for _, table := range tableNames {
		if col, ok := etl.LastSeenColumn(s, table); ok {
			v, err := etl.MaxColumn(sqlitePath, table, col)
			if err != nil {
				return err
			}
			if v > asOf {
				asOf = v
			}
		}
	}
	if asOf > 0 {
		if err := etl.WriteDataAsOf(sqlitePath, asOf); err != nil {
			return err
		}
	}

	// pivot：恰好 1 个 pivot_money_columns（与 cmd/etl 同约束）。
	var pivots []string
	for name, t := range s.DerivedTables {
		if t.Method == "pivot_money_columns" {
			pivots = append(pivots, name)
		}
	}
	if len(pivots) != 1 {
		return fmt.Errorf("v0.2 cmd/seed 需要恰好 1 个 pivot_money_columns 派生表，找到 %d", len(pivots))
	}
	dest := pivots[0]
	src := s.DerivedTables[dest].DerivedFrom
	srcSpec, ok := sp.Tables[src]
	if !ok {
		return fmt.Errorf("pivot 源表 %s 不在 seed spec 中", src)
	}
	money, err := etl.MoneyColumnsFor(s, src)
	if err != nil {
		return err
	}
	curRows := make([]etl.CurrencyRow, 0, srcSpec.Rows*len(money))
	for i := 0; i < srcSpec.Rows; i++ {
		pid := etl.HashPID(salt, int64(i+1)) // seq 1..rows：hash 唯一
		for _, m := range money {
			curRows = append(curRows, etl.CurrencyRow{
				PlayerID: pid, CurrencyType: m.CurrencyType,
				Balance: generated[src][m.Column][i],
			})
		}
	}
	if err := etl.LoadCurrencies(curRows, dest, sqlitePath, s.Version); err != nil {
		return fmt.Errorf("load %s: %w", dest, err)
	}

	h := etl_health.Health{
		Status: etl_health.StatusOK, Rows: int64(len(curRows)),
		FinishedAt: time.Now(), SchemaVersion: s.Version,
		Frozen: s.ETLPolicy.Frozen, DataAsOf: asOf,
	}
	if s.ETLPolicy.HealthMinRows > 0 {
		mr := s.ETLPolicy.HealthMinRows
		h.MinRowsOverride = &mr
	}
	return etl_health.Write(healthPath, h)
}
