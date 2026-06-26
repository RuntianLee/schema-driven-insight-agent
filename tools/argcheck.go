// Package tools — argcheck.go
// 未知参数键校验：tool-call args 含白名单外的键 → SCHEMA_ERROR + did-you-mean，
// 触发 agent 既有自修重试（而非静默忽略 → 返回错数据）。
// 白名单由 input 结构体的 json tag 反射派生（单一真值源，加字段自动扩展）。
package tools

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/RuntianLee/schema-driven-insight-agent/contract"
)

var analyzeKnownKeys = jsonTagKeys(reflect.TypeOf(AnalyzeInput{}))
var queryDistributionKnownKeys = jsonTagKeys(reflect.TypeOf(QueryDistributionInput{}))

// jsonTagKeys 取结构体 t 各导出字段的 json tag 名（剥 ",omitempty" 等选项；
// 无 tag 或 tag="-" 的字段忽略）。供白名单单一真值源派生。
func jsonTagKeys(t reflect.Type) []string {
	var keys []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		if comma := strings.IndexByte(tag, ','); comma >= 0 {
			tag = tag[:comma]
		}
		if tag != "" {
			keys = append(keys, tag)
		}
	}
	return keys
}

// checkUnknownArgs 扫描 args 的键；任一不在 known 白名单 → 返回 SCHEMA_ERROR Response
// （Hint 带 did-you-mean）。全部已知 → 返回 nil。
func checkUnknownArgs(args map[string]any, known []string, tool string) *contract.Response {
	knownSet := make(map[string]bool, len(known))
	for _, k := range known {
		knownSet[k] = true
	}
	var unknown []string
	for k := range args {
		if !knownSet[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	hints := make([]string, 0, len(unknown))
	for _, u := range unknown {
		if sug := suggestKey(u, known); sug != "" {
			hints = append(hints, fmt.Sprintf("unknown arg key %q; did you mean %q?", u, sug))
		} else {
			hints = append(hints, fmt.Sprintf("unknown arg key %q; valid keys: %s", u, strings.Join(known, ", ")))
		}
	}
	return &contract.Response{
		Status: contract.StatusSchemaError,
		Hint:   fmt.Sprintf("%s: %s", tool, strings.Join(hints, "; ")),
	}
}

// suggestKey 返回 known 中与 key 编辑距离 ≤2 的最近者；无则空串。
func suggestKey(key string, known []string) string {
	best := ""
	bestDist := 3
	for _, k := range known {
		if d := levenshtein(key, k); d < bestDist {
			bestDist = d
			best = k
		}
	}
	return best
}

// levenshtein 标准编辑距离（rune 级，两行滚动数组）。
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
}

// ParseAnalyzeArgs 校验未知键后解析 analyze args。未知键 → (零值, SCHEMA_ERROR)。
func ParseAnalyzeArgs(args map[string]any) (AnalyzeInput, *contract.Response) {
	if resp := checkUnknownArgs(args, analyzeKnownKeys, "analyze"); resp != nil {
		return AnalyzeInput{}, resp
	}
	return ArgsToAnalyzeInput(args), nil
}

// ParseQueryDistributionArgs 校验未知键后解析 query_distribution args。
func ParseQueryDistributionArgs(args map[string]any) (QueryDistributionInput, *contract.Response) {
	if resp := checkUnknownArgs(args, queryDistributionKnownKeys, "query_distribution"); resp != nil {
		return QueryDistributionInput{}, resp
	}
	return ArgsToQueryDistributionInput(args), nil
}
