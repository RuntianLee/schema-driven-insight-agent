// Package tools — args.go
// ArgsToQueryDistributionInput 把 dispatch 的 args（map[string]any，来自 LLM tool call JSON）
// 转成强类型 QueryDistributionInput。从 cmd/agent 私有版提取，供 agent + eval cmd 共用（DRY）。
package tools

import "github.com/RuntianLee/schema-driven-insight-agent/schema_protocol"

// ArgsToQueryDistributionInput converts LLM tool-call args (map[string]any) to a typed
// QueryDistributionInput. The logic is extracted from the private argsToQueryDistributionInput
// in framework/cmd/agent/main.go to allow reuse by both the agent CLI and the eval harness.
func ArgsToQueryDistributionInput(args map[string]any) QueryDistributionInput {
	in := QueryDistributionInput{}
	if v, ok := args["table"].(string); ok {
		in.Table = v
	}
	if v, ok := args["column"].(string); ok {
		in.Column = v
	}
	if v, ok := args["bucket_key"].(string); ok {
		in.BucketKey = v
	}
	if v, ok := args["filter"].(map[string]any); ok {
		in.Filter = v
	}
	if raw, ok := args["group_by"]; ok {
		switch v := raw.(type) {
		case []string:
			in.GroupBy = v
		case []any:
			for _, item := range v {
				if s, ok := item.(string); ok {
					in.GroupBy = append(in.GroupBy, s)
				}
			}
		}
	}
	return in
}

// ArgsToAnalyzeInput 把 LLM tool-call args（map[string]any）转成强类型 AnalyzeInput。
// JSON 数字解析为 float64、数组为 []any——本函数做容错转换，非法/缺失字段留零值，
// 真正的语义校验在 schema_protocol.BuildAnalysis（白名单/类型）统一兜底。
func ArgsToAnalyzeInput(args map[string]any) AnalyzeInput {
	in := AnalyzeInput{}
	if v, ok := args["table"].(string); ok {
		in.Table = v
	}
	in.Limit = asInt(args["limit"])
	in.GroupBy = asStringSlice(args["group_by"])

	for _, raw := range asSlice(args["filters"]) {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		f := schema_protocol.Filter{}
		f.Field, _ = m["field"].(string)
		f.Op, _ = m["op"].(string)
		if v, ok := m["value"]; ok {
			f.Value = v
		}
		f.Values = asSlice(m["values"])
		in.Filters = append(in.Filters, f)
	}

	for _, raw := range asSlice(args["aggregates"]) {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		a := schema_protocol.Aggregate{}
		a.Fn, _ = m["fn"].(string)
		a.Column, _ = m["column"].(string)
		a.As, _ = m["as"].(string)
		in.Aggregates = append(in.Aggregates, a)
	}

	for _, raw := range asSlice(args["having"]) {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		h := schema_protocol.HavingCond{}
		h.Alias, _ = m["alias"].(string)
		h.Op, _ = m["op"].(string)
		if v, ok := m["value"]; ok {
			h.Value = v
		}
		in.Having = append(in.Having, h)
	}

	for _, raw := range asSlice(args["order_by"]) {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		o := schema_protocol.OrderKey{}
		o.Key, _ = m["key"].(string)
		o.Desc, _ = m["desc"].(bool)
		in.OrderBy = append(in.OrderBy, o)
	}
	return in
}

func asSlice(v any) []any {
	if s, ok := v.([]any); ok {
		return s
	}
	return nil
}

func asStringSlice(v any) []string {
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		var out []string
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func asInt(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	}
	return 0
}
