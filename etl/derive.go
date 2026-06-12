// derive.go 从 schema 推导 ETL 装配参数——零代码接入 v0.2 的核心：
// 货币列/索引列/actor_id/last_seen 不再由 adapter Go 代码重复声明。
package etl

import (
	"fmt"
	"sort"

	"github.com/RuntianLee/schema-driven-insight-agent/schema_protocol"
)

// MoneyColumnsFor 从 state 表字段的 currency_type 标注推导 pivot 货币列（列名字母序，确定性）。
func MoneyColumnsFor(s *schema_protocol.Schema, table string) ([]MoneyColumn, error) {
	t, ok := s.StateTables[table]
	if !ok {
		return nil, fmt.Errorf("schema missing state_table %s", table)
	}
	var out []MoneyColumn
	for name, fd := range t.Fields {
		if fd.CurrencyType != "" {
			out = append(out, MoneyColumn{Column: name, CurrencyType: fd.CurrencyType})
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("state_table %s 无 currency_type 标注列，无法 pivot", table)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Column < out[j].Column })
	return out, nil
}

// IndexColumnsFor 推导建索引列（index: true 且物化），字母序。
func IndexColumnsFor(s *schema_protocol.Schema, table string) []string {
	t, ok := s.StateTables[table]
	if !ok {
		return nil
	}
	var out []string
	for name, fd := range t.Fields {
		if fd.Index && !fd.PII && !fd.OmitInLayer2 {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// ActorIDColumn 返回 state 表 role=actor_id 的列（pivot 脱敏 hash 的输入），须恰好一个。
func ActorIDColumn(s *schema_protocol.Schema, table string) (string, error) {
	t, ok := s.StateTables[table]
	if !ok {
		return "", fmt.Errorf("schema missing state_table %s", table)
	}
	var found []string
	for name, fd := range t.Fields {
		if fd.Role == "actor_id" {
			found = append(found, name)
		}
	}
	if len(found) != 1 {
		return "", fmt.Errorf("state_table %s 须恰有 1 个 role=actor_id 列，找到 %d", table, len(found))
	}
	return found[0], nil
}

// LastSeenColumn 返回 state 表已物化的 role=last_seen 列（data_as_of 锚点）；无则 ok=false。
func LastSeenColumn(s *schema_protocol.Schema, table string) (string, bool) {
	t, ok := s.StateTables[table]
	if !ok {
		return "", false
	}
	for name, fd := range t.Fields {
		if fd.Role == "last_seen" && !fd.PII && !fd.OmitInLayer2 {
			return name, true
		}
	}
	return "", false
}
