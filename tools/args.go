// Package tools — args.go
// ArgsToQueryDistributionInput 把 dispatch 的 args（map[string]any，来自 LLM tool call JSON）
// 转成强类型 QueryDistributionInput。从 cmd/agent 私有版提取，供 agent + eval cmd 共用（DRY）。
package tools

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
