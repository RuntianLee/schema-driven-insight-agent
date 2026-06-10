package etl

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Scope 声明 adapter 的数据范围过滤（如按 server 维度圈定）。来自 schema.yaml 顶层可选 scope 块。
// 注意：本包自行解析 scope（不经 framework/schema_protocol），以保冻结 core 0 改动。
type Scope struct {
	FilterColumn string  `yaml:"filter_column"`
	ServerIDs    []int64 `yaml:"server_ids"`
}

// scopeWrapper 仅取 schema.yaml 顶层 scope 块，其余键由 yaml.Unmarshal 忽略。
type scopeWrapper struct {
	Scope *Scope `yaml:"scope"`
}

// ParseScope 从 schema YAML 原始字节解析可选 scope。
// 无 scope 块 → (nil, nil)，调用方据此不加过滤（无 scope 路径）。
func ParseScope(yamlBytes []byte) (*Scope, error) {
	var w scopeWrapper
	if err := yaml.Unmarshal(yamlBytes, &w); err != nil {
		return nil, fmt.Errorf("parse scope: %w", err)
	}
	if w.Scope == nil {
		return nil, nil
	}
	if w.Scope.FilterColumn == "" {
		return nil, fmt.Errorf("scope.filter_column required when scope present")
	}
	if len(w.Scope.ServerIDs) == 0 {
		return nil, fmt.Errorf("scope.server_ids must be non-empty when scope present")
	}
	return w.Scope, nil
}

// WhereClause 构造 pgx 参数化 WHERE 子句（防注入）。
// nil scope → ("", nil)，使 SQL 与无过滤逐字一致。
func (sc *Scope) WhereClause() (string, []any) {
	if sc == nil {
		return "", nil
	}
	ph := make([]string, len(sc.ServerIDs))
	args := make([]any, len(sc.ServerIDs))
	for i, id := range sc.ServerIDs {
		ph[i] = fmt.Sprintf("$%d", i+1)
		args[i] = id
	}
	return fmt.Sprintf(" WHERE %s IN (%s)", sc.FilterColumn, strings.Join(ph, ", ")), args
}

// buildSelectQuery 统一拼 SELECT（basics/currencies 共用）。where 为空 → 无 WHERE。
func buildSelectQuery(table string, cols []string, where string) string {
	return fmt.Sprintf("SELECT %s FROM %s%s", strings.Join(cols, ", "), table, where)
}
