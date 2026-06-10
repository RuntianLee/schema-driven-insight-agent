package schema_protocol

import (
	"fmt"
	"sort"
	"strings"
)

// Digest 生成结构摘要（DETERMINISTIC，sorted），供 LLM system prompt 使用。
// 只含名字（table/column/role/bucket_key/currency_type），不含任何分布数字。
// design-v3 §4「不预投 baseline 数字」：Buckets 的 min/max 边界值故意省略。
//
// SP1 起：列出 state_tables 与 derived_tables 的可查询列（均已物化到 Layer2）。
// 只暴露非 PII 列（pii / omit_in_layer2 的列恒不出现）——PII 边界，见 spec §3.8。
func (s *Schema) Digest() string {
	var sb strings.Builder

	sb.WriteString("## 可查询数据（构造 query_distribution 参数时只用下列名字）\n")
	sb.WriteString("表与字段（均为 Layer2 已物化、可直接查询；仅列非 PII 列）：\n")

	// ── state tables（Layer2 已物化，仅非 PII 列）──
	stateNames := make([]string, 0, len(s.StateTables))
	for name := range s.StateTables {
		stateNames = append(stateNames, name)
	}
	sort.Strings(stateNames)
	for _, name := range stateNames {
		tbl := s.StateTables[name]
		sb.WriteString(fmt.Sprintf("- %s (state): %s\n", name, formatFields(tbl.Fields)))
	}

	// ── derived tables ──
	derivedNames := make([]string, 0, len(s.DerivedTables))
	for name := range s.DerivedTables {
		derivedNames = append(derivedNames, name)
	}
	sort.Strings(derivedNames)
	for _, name := range derivedNames {
		tbl := s.DerivedTables[name]
		sb.WriteString(fmt.Sprintf("- %s (derived): %s\n", name, formatFields(tbl.Fields)))
	}

	// ── bucket_key 可用名称（不含边界值）──
	bucketKeys := make([]string, 0, len(s.Glossary.Buckets))
	for key := range s.Glossary.Buckets {
		bucketKeys = append(bucketKeys, key)
	}
	sort.Strings(bucketKeys)
	sb.WriteString(fmt.Sprintf("可用 bucket_key：%s\n", strings.Join(bucketKeys, ", ")))

	// ── currency_type 合法取值（filter 用）──
	ctKeys := make([]string, 0, len(s.Glossary.CurrencyTypes))
	for key := range s.Glossary.CurrencyTypes {
		ctKeys = append(ctKeys, key)
	}
	sort.Strings(ctKeys)
	sb.WriteString(fmt.Sprintf("currency_type 取值（filter 用）：%s\n", strings.Join(ctKeys, ", ")))

	return sb.String()
}

// formatFields 返回有序的 "fieldName(role=xxx)" 列表，用逗号分隔。
// 跳过 PII 列（pii || omit_in_layer2）——绝不把 PII 列名暴露给 LLM。
func formatFields(fields map[string]FieldDef) string {
	names := make([]string, 0, len(fields))
	for name, fd := range fields {
		if fd.PII || fd.OmitInLayer2 {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)

	parts := make([]string, 0, len(names))
	for _, name := range names {
		fd := fields[name]
		if fd.Role != "" {
			parts = append(parts, fmt.Sprintf("%s(role=%s)", name, fd.Role))
		} else {
			parts = append(parts, name)
		}
	}
	return strings.Join(parts, ", ")
}
