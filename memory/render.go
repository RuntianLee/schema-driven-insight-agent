package memory

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const defaultContextMaxChars = 1200

// RenderContext formats scrubbed memory hits for prompt context.
func RenderContext(results []SearchResult, opts ContextOptions) string {
	if len(results) == 0 {
		return ""
	}

	maxItems := opts.MaxItems
	if maxItems <= 0 || maxItems > len(results) {
		maxItems = len(results)
	}
	maxChars := opts.MaxChars
	if maxChars <= 0 {
		maxChars = defaultContextMaxChars
	}

	var b strings.Builder
	b.WriteString("Memory context（已脱敏，可作为参考案例，不作为事实来源）:")
	for i := 0; i < maxItems; i++ {
		item := results[i].Item
		fmt.Fprintf(&b, "\n- [%s] Q: %s", item.ID, item.Question)
		if item.Summary != "" {
			fmt.Fprintf(&b, "\n  Summary: %s", item.Summary)
		}
		if item.AnswerOutline != "" {
			fmt.Fprintf(&b, "\n  Path: %s", item.AnswerOutline)
		}
		if len(item.Tools) > 0 {
			fmt.Fprintf(&b, "\n  Tools: %s", strings.Join(item.Tools, ", "))
		}
	}

	return truncateRunes(b.String(), maxChars)
}

func truncateRunes(s string, max int) string {
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max])
}
