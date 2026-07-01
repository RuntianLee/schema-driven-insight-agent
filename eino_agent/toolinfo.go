package eino_agent

import "github.com/cloudwego/eino/schema"

// ToolInfos 返回两个 agent 工具的结构化声明，供 Eino ChatModel.WithTools 使用。
//
// 精度选择：medium-fidelity——required 字段严格声明，复杂嵌套参数（filter/aggregates/
// filters/having/order_by）声明为 object/array 但不展开子字段，避免过严 schema 拒绝
// 模型的合法输出。详细语义仍以 prompts/system_v0.md prose 为权威。
func ToolInfos() []*schema.ToolInfo {
	return []*schema.ToolInfo{
		queryDistributionToolInfo(),
		analyzeToolInfo(),
	}
}

// queryDistributionToolInfo 声明 query_distribution 工具的参数 schema。
// 参数：table(required), column(required), bucket_key(opt), filter(object,opt), group_by(array<string>,opt)
func queryDistributionToolInfo() *schema.ToolInfo {
	return &schema.ToolInfo{
		Name: "query_distribution",
		Desc: "查询指定列的分布（直方图/频次），支持过滤和分组。用于分析单列数值或枚举分布。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"table": {
				Type:     schema.String,
				Desc:     "要查询的表名",
				Required: true,
			},
			"column": {
				Type:     schema.String,
				Desc:     "要统计分布的列名",
				Required: true,
			},
			"bucket_key": {
				Type: schema.String,
				Desc: "分桶 key（可选），指定分桶策略",
			},
			"filter": {
				Type: schema.Object,
				Desc: "过滤条件（可选），key-value 对象，字段名→值",
			},
			"group_by": {
				Type:     schema.Array,
				Desc:     "分组字段列表（可选），字符串数组",
				ElemInfo: &schema.ParameterInfo{Type: schema.String},
			},
		}),
	}
}

// analyzeToolInfo 声明 analyze 工具的参数 schema。
// 参数：table(required), group_by, aggregates, filters, having, order_by, limit
func analyzeToolInfo() *schema.ToolInfo {
	return &schema.ToolInfo{
		Name: "analyze",
		Desc: "对数据库表执行聚合分析查询，支持分组、聚合函数、过滤、having、排序、分页。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"table": {
				Type:     schema.String,
				Desc:     "要查询的表名",
				Required: true,
			},
			"group_by": {
				Type:     schema.Array,
				Desc:     "分组字段列表（可选），字符串数组",
				ElemInfo: &schema.ParameterInfo{Type: schema.String},
			},
			"aggregates": {
				Type:     schema.Array,
				Desc:     "聚合函数列表（可选），每项含 fn/column/as 字段的对象",
				ElemInfo: &schema.ParameterInfo{Type: schema.Object},
			},
			"filters": {
				Type:     schema.Array,
				Desc:     "行过滤条件列表（可选），每项含 field/op/value 字段的对象",
				ElemInfo: &schema.ParameterInfo{Type: schema.Object},
			},
			"having": {
				Type:     schema.Array,
				Desc:     "聚合后过滤条件列表（可选），每项含 alias/op/value 字段的对象",
				ElemInfo: &schema.ParameterInfo{Type: schema.Object},
			},
			"order_by": {
				Type:     schema.Array,
				Desc:     "排序规则列表（可选），每项含 key/desc 字段的对象",
				ElemInfo: &schema.ParameterInfo{Type: schema.Object},
			},
			"limit": {
				Type: schema.Integer,
				Desc: "返回行数上限（可选）",
			},
		}),
	}
}
