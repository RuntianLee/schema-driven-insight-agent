package trajectory

import (
	"encoding/json"
	"regexp"
)

// Event 是写入前的中间表示（trajectory-spec-v2 §11）。
type Event struct {
	Kind     string
	Input    any
	Output   any
	Metadata map[string]any
}

var (
	// 16 字符 hex → player id hash 候选（含大写）
	rePlayerHash = regexp.MustCompile(`\b[a-fA-F0-9]{16}\b`)
	// SQL where 字面量 > 20 字节
	reSQLLiteral = regexp.MustCompile(`'[^']{21,}'`)
	// player/id/玩家 邻接 6+ 位数字
	rePlayerDigits = regexp.MustCompile(`(?i)(player|id|玩家)(\D{0,4})(\d{6,})`)
)

const maxSampleRows = 10

// normalize 通过 JSON round-trip 将任意类型（struct、typed slice、typed map 等）
// 转换为 redactValue 可深度遍历的 map[string]any / []any / primitive 形式。
// marshal/unmarshal 失败时原样返回，不影响主流程。
func normalize(v any) any {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		return v
	}
	return out
}

// Redact 返回新 Event，不修改原值（不可变原则）。纯函数，可单测。
// Input/Output 先经 normalize 转为泛型结构，再由 redactValue 深度遍历脱敏。
func Redact(e Event) Event {
	return Event{
		Kind:     e.Kind,
		Input:    redactValue(normalize(e.Input)),
		Output:   redactValue(normalize(e.Output)),
		Metadata: e.Metadata,
	}
}

func redactValue(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case string:
		return redactString(t)
	case []any:
		trimmed := t
		if len(trimmed) > maxSampleRows {
			trimmed = trimmed[:maxSampleRows]
		}
		out := make([]any, len(trimmed))
		for i, e := range trimmed {
			out[i] = redactValue(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = redactValue(e)
		}
		return out
	default:
		return v
	}
}

func redactString(s string) string {
	s = rePlayerHash.ReplaceAllString(s, "<player>")
	s = reSQLLiteral.ReplaceAllString(s, "'?'")
	s = rePlayerDigits.ReplaceAllString(s, "${1}${2}<id>")
	return s
}
