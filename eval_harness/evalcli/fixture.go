// fixture.go：任务 YAML 内联 fixture（零代码接入 v0.2）——取代 per-adapter 的
// Go fixture 函数。按 schema 物化列建表（PII 列天然不存在），按 groups 插入；
// values 出现未知列/PII 列即报错拒跑。
//
// 任务 YAML fixture 契约：
//
//	fixture:
//	  tables:
//	    player_basics:           # 仅支持 state 表；建表含全部物化列，未指定列为 NULL
//	      groups:                # 原 Go fixture 的 insertN 模式 1:1 对应
//	        - {count: 120, values: {server_id: 1, level: 50, passed_main_stage_id: 10, last_online_time: 1716000000}}
package evalcli

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/RuntianLee/schema-driven-insight-agent/etl"
	"github.com/RuntianLee/schema-driven-insight-agent/schema_protocol"
	"gopkg.in/yaml.v3"
)

type fixtureSpec struct {
	Tables map[string]fixtureTable `yaml:"tables"`
}

type fixtureTable struct {
	Groups []fixtureGroup `yaml:"groups"`
}

type fixtureGroup struct {
	Count  int            `yaml:"count"`
	Values map[string]any `yaml:"values"`
}

// buildFixtureDB 在 dir 下建 fixture.db 并按 spec 填充，返回已就绪连接（调用方 Close）。
func buildFixtureDB(s *schema_protocol.Schema, node yaml.Node, dir string) (*sql.DB, error) {
	var spec fixtureSpec
	if err := node.Decode(&spec); err != nil {
		return nil, fmt.Errorf("fixture 块解析: %w", err)
	}
	if len(spec.Tables) == 0 {
		return nil, fmt.Errorf("fixture.tables 为空")
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "fixture.db"))
	if err != nil {
		return nil, err
	}
	tableNames := make([]string, 0, len(spec.Tables))
	for k := range spec.Tables {
		tableNames = append(tableNames, k)
	}
	sort.Strings(tableNames)
	for _, table := range tableNames {
		if err := buildFixtureTable(db, s, table, spec.Tables[table]); err != nil {
			db.Close()
			return nil, err
		}
	}
	if err := buildFixtureDerivedTables(db, s); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func buildFixtureTable(db *sql.DB, s *schema_protocol.Schema, table string, ft fixtureTable) error {
	cols, err := etl.BasicsColumns(s, table) // 物化列全集（v0.2 仅支持 state 表 fixture）
	if err != nil {
		return fmt.Errorf("fixture 表 %s: %w（v0.2 仅支持 state 表）", table, err)
	}
	colIdx := make(map[string]int, len(cols))
	defs := make([]string, len(cols))
	for i, c := range cols {
		colIdx[c.Name] = i
		defs[i] = c.Name + " " + c.SQLiteType
	}
	if _, err := db.Exec(fmt.Sprintf(`CREATE TABLE %s (%s)`, table, strings.Join(defs, ", "))); err != nil {
		return fmt.Errorf("create %s: %w", table, err)
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(cols)), ",")
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(fmt.Sprintf(`INSERT INTO %s VALUES (%s)`, table, ph))
	if err != nil {
		return err
	}
	defer stmt.Close()
	for gi, g := range ft.Groups {
		if g.Count <= 0 {
			return fmt.Errorf("fixture %s groups[%d]: count 必须 > 0", table, gi)
		}
		row := make([]any, len(cols)) // 未指定列 → NULL
		for col, v := range g.Values {
			idx, ok := colIdx[col]
			if !ok {
				return fmt.Errorf("fixture %s groups[%d]: 列 %q 不可用（PII/未物化/不存在）", table, gi, col)
			}
			row[idx] = v
		}
		for k := 0; k < g.Count; k++ {
			if _, err := stmt.Exec(row...); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func buildFixtureDerivedTables(db *sql.DB, s *schema_protocol.Schema) error {
	derivedNames := make([]string, 0, len(s.DerivedTables))
	for name := range s.DerivedTables {
		derivedNames = append(derivedNames, name)
	}
	sort.Strings(derivedNames)
	for _, name := range derivedNames {
		tbl := s.DerivedTables[name]
		switch tbl.Method {
		case "pivot_money_columns":
			if err := buildFixturePivotMoneyTable(db, s, name, tbl); err != nil {
				return fmt.Errorf("fixture 派生表 %s: %w", name, err)
			}
		case "":
			continue
		default:
			return fmt.Errorf("fixture 暂不支持派生表 %s method=%q", name, tbl.Method)
		}
	}
	return nil
}

func buildFixturePivotMoneyTable(db *sql.DB, s *schema_protocol.Schema, dest string, tbl schema_protocol.Table) error {
	if tbl.DerivedFrom == "" {
		return fmt.Errorf("缺少 derived_from")
	}
	exists, err := fixtureTableExists(db, tbl.DerivedFrom)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	pidCol, err := singleFieldByRole(tbl.Fields, "actor_id")
	if err != nil {
		return err
	}
	kindCol, err := singleFieldByRole(tbl.Fields, "currency_kind")
	if err != nil {
		return err
	}
	balanceCol, err := singleFieldByRole(tbl.Fields, "balance")
	if err != nil {
		return err
	}
	moneyCols, err := etl.MoneyColumnsFor(s, tbl.DerivedFrom)
	if err != nil {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(fmt.Sprintf(`DROP TABLE IF EXISTS %s`, dest)); err != nil {
		return err
	}
	if _, err := tx.Exec(fmt.Sprintf(`CREATE TABLE %s (%s TEXT, %s TEXT, %s INTEGER)`, dest, pidCol, kindCol, balanceCol)); err != nil {
		return err
	}
	for _, mc := range moneyCols {
		if _, err := tx.Exec(
			fmt.Sprintf(`INSERT INTO %s (%s, %s, %s) SELECT CAST(rowid AS TEXT), ?, %s FROM %s`, dest, pidCol, kindCol, balanceCol, mc.Column, tbl.DerivedFrom),
			mc.CurrencyType,
		); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(fmt.Sprintf(`CREATE INDEX idx_%s_%s_%s ON %s(%s, %s)`, dest, kindCol, balanceCol, dest, kindCol, balanceCol)); err != nil {
		return err
	}
	return tx.Commit()
}

func fixtureTableExists(db *sql.DB, table string) (bool, error) {
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

func singleFieldByRole(fields map[string]schema_protocol.FieldDef, role string) (string, error) {
	var found []string
	for name, fd := range fields {
		if fd.Role == role {
			found = append(found, name)
		}
	}
	sort.Strings(found)
	if len(found) != 1 {
		return "", fmt.Errorf("role=%s 字段数量=%d，期望 1", role, len(found))
	}
	return found[0], nil
}
