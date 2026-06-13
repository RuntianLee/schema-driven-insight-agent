package csvload

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/RuntianLee/schema-driven-insight-agent/etl"
	"github.com/RuntianLee/schema-driven-insight-agent/etl_health"
	"github.com/RuntianLee/schema-driven-insight-agent/schema_protocol"
)

// RunOptions 驱动一次 CSV → Layer-2 构建。路径解析规则与 cmd/etl / cmd/seed 一致（etl.ResolveOutputs）。
type RunOptions struct {
	SchemaPath string
	CSVPath    string // 空 → 从 schema data_sources type=csv 的 path 解析
	SQLitePath string
	HealthPath string
}

// Run：解析 schema → 读 CSV → 物化 state 表（脱敏剔除 PII 列）→ data_as_of
// → pivot 货币列（actor_id 经 HashPID 脱敏）→ LoadCurrencies → health。
// 镜像 etl.RunAll / seedgen.Run 的顺序与 health 语义。
// v0.3 约束：恰好 1 个 state 表 ↔ 1 份 CSV；恰好 1 个 pivot_money_columns 派生表。
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
		return fmt.Errorf("schema 缺 etl_policy 块（cmd/csv 需要 hash_salt 等）")
	}
	if len(s.StateTables) != 1 {
		return fmt.Errorf("v0.3 cmd/csv 需要恰好 1 个 state_table，找到 %d", len(s.StateTables))
	}
	var table string
	for k := range s.StateTables {
		table = k
	}

	csvPath := o.CSVPath
	if csvPath == "" {
		if csvPath, err = CSVPathFromSchema(s, o.SchemaPath); err != nil {
			return err
		}
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

	header, records, err := readCSV(csvPath)
	if err != nil {
		return err
	}
	idx := make(map[string]int, len(header))
	for i, h := range header {
		idx[strings.TrimSpace(h)] = i
	}
	if err := etl.ValidateRowCount(len(records), s.ETLPolicy.MinRows); err != nil {
		return err
	}

	cols, err := etl.BasicsColumns(s, table)
	if err != nil {
		return err
	}
	for _, c := range cols {
		if _, ok := idx[c.Name]; !ok {
			return fmt.Errorf("CSV 缺物化列表头 %q", c.Name)
		}
	}
	rows := make([][]any, len(records))
	for i, rec := range records {
		row := make([]any, len(cols))
		for j, c := range cols {
			v, cerr := coerce(rec[idx[c.Name]], c.SQLiteType)
			if cerr != nil {
				return fmt.Errorf("行 %d 列 %s: %w", i+2, c.Name, cerr)
			}
			row[j] = v
		}
		rows[i] = row
	}
	if err := etl.LoadBasics(rows, cols, table, sqlitePath, etl.IndexColumnsFor(s, table)); err != nil {
		return fmt.Errorf("load %s: %w", table, err)
	}

	var asOf int64
	if col, ok := etl.LastSeenColumn(s, table); ok {
		if asOf, err = etl.MaxColumn(sqlitePath, table, col); err != nil {
			return err
		}
	}
	if asOf == 0 {
		asOf = s.ETLPolicy.DataAsOf
	}
	if asOf > 0 {
		if err := etl.WriteDataAsOf(sqlitePath, asOf); err != nil {
			return err
		}
	}

	var pivots []string
	for name, t := range s.DerivedTables {
		if t.Method == "pivot_money_columns" {
			pivots = append(pivots, name)
		}
	}
	if len(pivots) != 1 {
		return fmt.Errorf("v0.3 cmd/csv 需要恰好 1 个 pivot_money_columns 派生表，找到 %d", len(pivots))
	}
	dest := pivots[0]
	src := s.DerivedTables[dest].DerivedFrom
	if src != table {
		return fmt.Errorf("pivot 源表 %q 与 CSV state 表 %q 不一致", src, table)
	}
	pidCol, err := etl.ActorIDColumn(s, src)
	if err != nil {
		return err
	}
	if _, ok := idx[pidCol]; !ok {
		return fmt.Errorf("CSV 缺 actor_id 列表头 %q", pidCol)
	}
	money, err := etl.MoneyColumnsFor(s, src)
	if err != nil {
		return err
	}
	for _, m := range money {
		if _, ok := idx[m.Column]; !ok {
			return fmt.Errorf("CSV 缺货币列表头 %q", m.Column)
		}
	}
	curRows := make([]etl.CurrencyRow, 0, len(records)*len(money))
	for i, rec := range records {
		pidV, perr := coerce(rec[idx[pidCol]], "INTEGER")
		if perr != nil {
			return fmt.Errorf("行 %d actor_id %s: %w", i+2, pidCol, perr)
		}
		h := etl.HashPID(salt, pidV.(int64))
		for _, m := range money {
			balV, berr := coerce(rec[idx[m.Column]], "INTEGER")
			if berr != nil {
				return fmt.Errorf("行 %d 货币 %s: %w", i+2, m.Column, berr)
			}
			curRows = append(curRows, etl.CurrencyRow{PlayerID: h, CurrencyType: m.CurrencyType, Balance: balV.(int64)})
		}
	}
	if err := etl.LoadCurrencies(curRows, dest, sqlitePath, s.Version); err != nil {
		return fmt.Errorf("load %s: %w", dest, err)
	}

	hlt := etl_health.Health{
		Status:        etl_health.StatusOK,
		Rows:          int64(len(curRows)),
		FinishedAt:    time.Now(),
		SchemaVersion: s.Version,
		Frozen:        s.ETLPolicy.Frozen,
		DataAsOf:      asOf,
	}
	if s.ETLPolicy.HealthMinRows > 0 {
		mr := s.ETLPolicy.HealthMinRows
		hlt.MinRowsOverride = &mr
	}
	return etl_health.Write(healthPath, hlt)
}
