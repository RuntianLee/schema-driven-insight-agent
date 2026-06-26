package tools

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/RuntianLee/schema-driven-insight-agent/contract"
	"github.com/RuntianLee/schema-driven-insight-agent/schema_protocol"
)

// AnalyzeInput 是 analyze 工具的输入（镜像 schema_protocol.AnalysisQuery）。
type AnalyzeInput struct {
	Table      string                       `json:"table"`
	Filters    []schema_protocol.Filter     `json:"filters"`
	GroupBy    []string                     `json:"group_by"`
	Aggregates []schema_protocol.Aggregate  `json:"aggregates"`
	Having     []schema_protocol.HavingCond `json:"having"`
	OrderBy    []schema_protocol.OrderKey   `json:"order_by"`
	Limit      int                          `json:"limit"`
}

// Query 把 AnalyzeInput 转换为 schema_protocol.AnalysisQuery，供内部及上层直接复用。
func (in AnalyzeInput) Query() schema_protocol.AnalysisQuery {
	return schema_protocol.AnalysisQuery{
		Table: in.Table, Filters: in.Filters, GroupBy: in.GroupBy,
		Aggregates: in.Aggregates, Having: in.Having, OrderBy: in.OrderBy, Limit: in.Limit,
	}
}

// AnalyzeTool 执行通用可组合查询（V2 路线 B）。Layer2 只读 SQLite。
type AnalyzeTool struct {
	schema *schema_protocol.Schema
	db     *sql.DB
}

func NewAnalyzeTool(s *schema_protocol.Schema, db *sql.DB) *AnalyzeTool {
	return &AnalyzeTool{schema: s, db: db}
}

// Run 不返回 error：四状态覆盖全部失败语义（design-v3 §10）。
// 校验失败 → SCHEMA_ERROR + hint 让 agent 自修正；执行成功 → OK + Table。
func (t *AnalyzeTool) Run(ctx context.Context, in AnalyzeInput) contract.Response {
	q := in.Query()
	sqlText, args, err := t.schema.BuildAnalysis(q)
	if err != nil {
		var se *schema_protocol.SchemaError
		if errors.As(err, &se) {
			return contract.Response{Status: contract.StatusSchemaError, Hint: se.Hint, SchemaPath: se.Path}
		}
		return contract.Response{Status: contract.StatusSchemaError, Hint: err.Error()}
	}

	rows, err := t.db.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return contract.Response{Status: contract.StatusSchemaError, Hint: fmt.Sprintf("query failed: %v", err)}
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return contract.Response{Status: contract.StatusSchemaError, Hint: err.Error()}
	}
	colMeta := t.columnMeta(in.Table, cols)

	var out [][]any
	for rows.Next() {
		cells := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range cells {
			ptrs[i] = &cells[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return contract.Response{Status: contract.StatusSchemaError, Hint: err.Error()}
		}
		for i := range cells {
			cells[i] = normalizeCell(cells[i])
		}
		out = append(out, cells)
	}
	if err := rows.Err(); err != nil {
		return contract.Response{Status: contract.StatusSchemaError, Hint: err.Error()}
	}

	return contract.Response{
		Status: contract.StatusOK,
		Table:  &contract.TableResult{Columns: colMeta, Rows: out, RowCount: int64(len(out))},
	}
}

// columnMeta 为输出列附 schema 字段类型（group_by 列匹配 schema 字段；聚合别名留空 Type）。
func (t *AnalyzeTool) columnMeta(table string, cols []string) []contract.ColumnMeta {
	var fields map[string]schema_protocol.FieldDef
	if t.schema != nil {
		if tbl, ok := t.schema.StateTables[table]; ok {
			fields = tbl.Fields
		} else if tbl, ok := t.schema.DerivedTables[table]; ok {
			fields = tbl.Fields
		}
	}
	meta := make([]contract.ColumnMeta, len(cols))
	for i, c := range cols {
		meta[i] = contract.ColumnMeta{Name: c}
		if fd, ok := fields[c]; ok {
			meta[i].Type = fd.Type
		}
	}
	return meta
}

// normalizeCell 把 SQLite 文本（[]byte）归一为 string；其余（int64/float64/nil）原样。
func normalizeCell(v any) any {
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	return v
}
