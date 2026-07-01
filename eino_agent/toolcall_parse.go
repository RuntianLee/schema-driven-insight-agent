package eino_agent

import (
	"bytes"
	"encoding/json"
	"html"
	"regexp"
	"strings"
)

type toolCall struct {
	Tool string         `json:"tool"`
	Args map[string]any `json:"args"`
}

// detectors 是工具调用格式探测器链，按结构签名特异性从高到低排列；
// 首个命中即返回。纯散文（无任何命中）→ 当最终答案。
// 设计=vLLM tool-parser 注册表的 Go 移植（per-format parser + auto-detect + 归一）。
var detectors = []func(string) (toolCall, bool){
	parseMinimaxXMLToolCall,  // 家族C：<invoke> XML（args-blob 既有 + 逐参数）
	parseTaggedJSONToolCall,  // 家族B：<tool_call>{json}</tool_call> / [TOOL_CALLS][{json}]
	parseOpenAIJSONToolCall,  // 家族A：{name, arguments/parameters/input}
	parseProjectJSONToolCall, // 家族A：{tool, args}（项目自有，既有路径原样保留）
}

// parseToolCall 依次尝试各格式探测器，把 LLM 文本输出解析为工具调用。
func parseToolCall(s string) (toolCall, bool) {
	trimmed := strings.TrimSpace(s)
	for _, d := range detectors {
		if c, ok := d(trimmed); ok {
			return c, true
		}
	}
	return toolCall{}, false
}

// parseProjectJSONToolCall 解析项目自有格式 {"tool":"X","args":{...}}（家族A）。
// 完全保留重构前的 JSON 解析逻辑：首个 { 起 Decoder 解单值（容忍尾部散文），
// 失败再补裸键引号重试。decodeToolCall 要求 tool 非空，故只认本格式、不与 OpenAI 抢。
func parseProjectJSONToolCall(s string) (toolCall, bool) {
	start := strings.Index(s, "{")
	if start < 0 {
		return toolCall{}, false
	}
	if c, ok := decodeToolCall(s[start:]); ok {
		return c, true
	}
	return decodeToolCall(quoteBareJSONKeys(s[start:]))
}

// toolCallTagRe 抓 <tool_call>…</tool_call> 内层（Hermes/Qwen）。
var toolCallTagRe = regexp.MustCompile(`(?s)<tool_call>\s*(.*?)\s*</tool_call>`)

// mistralTagRe 抓 [TOOL_CALLS] 之后到串尾的内容（Mistral/Llama 风格）。
// (.*) 贪婪吸到串尾的尾部散文由 decodeTaggedInner 的 json.Decoder 容忍（停在合法 JSON 尾）。
var mistralTagRe = regexp.MustCompile(`(?s)\[TOOL_CALLS\]\s*(.*)`)

// parseTaggedJSONToolCall 解析家族B：标记包裹的 JSON 工具调用。
// 内层走 toolCallFromObject（deferToProject=false：标记已是强信号、不让位）。
func parseTaggedJSONToolCall(s string) (toolCall, bool) {
	if m := toolCallTagRe.FindStringSubmatch(s); len(m) == 2 {
		if c, ok := decodeTaggedInner(m[1]); ok {
			return c, true
		}
	}
	if m := mistralTagRe.FindStringSubmatch(s); len(m) == 2 {
		if c, ok := decodeTaggedInner(m[1]); ok {
			return c, true
		}
	}
	return toolCall{}, false
}

// decodeTaggedInner 解析标记内层：数组 [{...}] 取第一个对象，或单个 {...}。
func decodeTaggedInner(inner string) (toolCall, bool) {
	inner = strings.TrimSpace(inner)
	if strings.HasPrefix(inner, "[") {
		var arr []json.RawMessage
		if json.NewDecoder(strings.NewReader(inner)).Decode(&arr) == nil && len(arr) > 0 {
			return toolCallFromObject(arr[0], false)
		}
		return toolCall{}, false
	}
	start := strings.Index(inner, "{")
	if start < 0 {
		return toolCall{}, false
	}
	return toolCallFromObject([]byte(inner[start:]), false)
}

// parseOpenAIJSONToolCall 解析 OpenAI 式 {name, arguments/parameters/input}（家族A）。
// 首个 { 起；含 tool 键则让位给项目探测器。补裸键引号重试以容错。
func parseOpenAIJSONToolCall(s string) (toolCall, bool) {
	start := strings.Index(s, "{")
	if start < 0 {
		return toolCall{}, false
	}
	if c, ok := toolCallFromObject([]byte(s[start:]), true); ok {
		return c, true
	}
	return toolCallFromObject([]byte(quoteBareJSONKeys(s[start:])), true)
}

// toolCallFromObject 从一段工具调用 JSON 对象解析 {name, arguments/parameters/input}，
// 供家族A（OpenAI 纯 JSON）与家族B（标记包裹）共用。用 Decoder 容忍尾部散文。
// deferToProject=true 时，对象含 "tool" 键则返回 false（让位给项目自有 {tool,args}）。
// arguments 兼容对象形态与 JSON 字符串形态（OpenAI 把 arguments 序列化成字符串）。
func toolCallFromObject(obj []byte, deferToProject bool) (toolCall, bool) {
	var raw map[string]json.RawMessage
	if json.NewDecoder(bytes.NewReader(obj)).Decode(&raw) != nil {
		return toolCall{}, false
	}
	if deferToProject {
		if _, has := raw["tool"]; has {
			return toolCall{}, false
		}
	}
	nameRaw, ok := raw["name"]
	if !ok {
		return toolCall{}, false
	}
	var name string
	if json.Unmarshal(nameRaw, &name) != nil || name == "" {
		return toolCall{}, false
	}
	args := map[string]any{}
	for _, k := range []string{"arguments", "parameters", "input"} {
		argRaw, ok := raw[k]
		if !ok {
			continue
		}
		var asObj map[string]any
		if json.Unmarshal(argRaw, &asObj) == nil {
			args = asObj // 对象形态
		} else {
			var asStr string
			if json.Unmarshal(argRaw, &asStr) == nil {
				_ = json.Unmarshal([]byte(asStr), &args) // JSON 字符串形态
			}
		}
		break
	}
	if args == nil {
		args = map[string]any{}
	}
	return toolCall{Tool: name, Args: args}, true
}

var minimaxXMLToolCallPattern = regexp.MustCompile(`(?s)<invoke\s+name=["']([^"']+)["'][^>]*>.*?<parameter\s+name=["']args["'][^>]*>(.*?)</parameter>`)

// invokeBlockRe 抓第一个 <invoke name="X">…</invoke> 块（含其内部 body）。
var invokeBlockRe = regexp.MustCompile(`(?s)<invoke\s+name=["']([^"']+)["'][^>]*>(.*?)</invoke>`)

// paramRe 抓 <parameter name="K">V</parameter>（逐参数形态）。
var paramRe = regexp.MustCompile(`(?s)<parameter\s+name=["']([^"']+)["'][^>]*>(.*?)</parameter>`)

// parseMinimaxXMLToolCall 解析 MiniMax 原生 XML 工具调用（家族C）。
// 先试既有 args-blob 形态（单个 name="args" 内含整块 JSON），不命中则逐参数兜底：
// 取第一个 <invoke>，把其所有 <parameter name="K">V</parameter> 聚成 args，
// 每个 V 按 JSON 解码（数组/对象/数字/bool），失败当字符串标量。
func parseMinimaxXMLToolCall(s string) (toolCall, bool) {
	// 1) args-blob 既有路径优先（零回归）。
	if m := minimaxXMLToolCallPattern.FindStringSubmatch(s); len(m) == 3 {
		argsText := strings.TrimSpace(html.UnescapeString(m[2]))
		if args, ok := decodeToolArgs(argsText); ok {
			return toolCall{Tool: m[1], Args: args}, true
		}
		if args, ok := decodeToolArgs(quoteBareJSONKeys(argsText)); ok {
			return toolCall{Tool: m[1], Args: args}, true
		}
		// args-blob 命中签名但 JSON 坏 → 继续逐参数兜底，不在此 return false。
		// 降级后逐参数会把 name="args" 当普通 key（args["args"]=raw），属可接受的行为降级。
	}

	// 2) 逐参数兜底：第一个 <invoke> + 其 <parameter>。
	ib := invokeBlockRe.FindStringSubmatch(s)
	if len(ib) != 3 {
		return toolCall{}, false
	}
	params := paramRe.FindAllStringSubmatch(ib[2], -1)
	if len(params) == 0 {
		return toolCall{}, false
	}
	args := make(map[string]any, len(params))
	for _, p := range params {
		args[p[1]] = decodeParamValue(strings.TrimSpace(html.UnescapeString(p[2])))
	}
	return toolCall{Tool: ib[1], Args: args}, true
}

// decodeParamValue 尝试把逐参数值按 JSON 严格解码（[]/{}/数字/bool/null，必须消耗全部输入），
// 失败则当字符串标量——避免 "42abc" 被误解成 42 而丢尾部（静默错值）。
func decodeParamValue(v string) any {
	var out any
	if json.Unmarshal([]byte(v), &out) == nil {
		return out
	}
	return v
}

func decodeToolArgs(s string) (map[string]any, bool) {
	var args map[string]any
	dec := json.NewDecoder(strings.NewReader(s))
	if err := dec.Decode(&args); err != nil {
		return nil, false
	}
	return args, true
}

func decodeToolCall(s string) (toolCall, bool) {
	var c toolCall
	dec := json.NewDecoder(strings.NewReader(s))
	if err := dec.Decode(&c); err != nil {
		return toolCall{}, false
	}
	if c.Tool == "" {
		return toolCall{}, false
	}
	return c, true
}

var bareJSONKeyPattern = regexp.MustCompile(`([{\s,])([A-Za-z_][A-Za-z0-9_]*)\s*:`)

func quoteBareJSONKeys(s string) string {
	return bareJSONKeyPattern.ReplaceAllString(s, `$1"$2":`)
}

// canonicalToolKey 把 tool 调用规范化为去重 key：tool 名 + args 的 JSON。
// encoding/json 对 map 键按字母序稳定输出，故同一 (tool, args) 不论 LLM 输出时
// 键序如何，得到的 key 一致（嵌套 args 如 filter 同理）；用于检测"完全相同的查询"。
func canonicalToolKey(c toolCall) string {
	b, err := json.Marshal(c)
	if err != nil {
		return c.Tool // 退化：marshal 失败（极少见）时仅按 tool 名
	}
	return string(b)
}
