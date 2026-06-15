package memory

import "github.com/RuntianLee/schema-driven-insight-agent/trajectory"

// ScrubText applies the shared trajectory redaction rules to memory text.
func ScrubText(s string) string {
	out := trajectory.Redact(trajectory.Event{Kind: "memory", Input: s})
	if redacted, ok := out.Input.(string); ok {
		return redacted
	}
	return s
}

// ScrubItem removes sensitive text before a memory item is persisted.
func ScrubItem(item Item) Item {
	item.Question = ScrubText(item.Question)
	item.Summary = ScrubText(item.Summary)
	item.AnswerOutline = ScrubText(item.AnswerOutline)
	return item
}
