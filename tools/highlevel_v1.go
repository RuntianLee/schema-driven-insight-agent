package tools

// RegisterHighLevelTools 在 V1 注册 8 个高层 tool（趋势 / 跨货币对比 / 大 R 流失等）。
// V0 仅 query_distribution 单 tool（见 query_distribution.go）。占位防重构。
func RegisterHighLevelTools(r *Registry) {
	// TODO(V1): 注册 design-v3 §附 数据源清单中的 #1/#3/#5 等高层 tool。
	_ = r
}
