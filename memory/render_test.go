package memory

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRenderContext_LimitsItemsAndChars(t *testing.T) {
	results := []SearchResult{
		{Item: Item{ID: "mem-1", Question: "first question", Summary: "first summary", AnswerOutline: "first path", Tools: []string{"analyze"}}},
		{Item: Item{ID: "mem-2", Question: "second question", Summary: "second summary"}},
	}

	got := RenderContext(results, ContextOptions{MaxItems: 1, MaxChars: 160})

	if utf8.RuneCountInString(got) > 160 {
		t.Fatalf("RenderContext exceeded MaxChars: len=%d output=%q", utf8.RuneCountInString(got), got)
	}
	if !strings.Contains(got, "Memory") {
		t.Fatalf("RenderContext should include header: %q", got)
	}
	if !strings.Contains(got, "mem-1") {
		t.Fatalf("RenderContext should include first item id: %q", got)
	}
	if strings.Contains(got, "mem-2") || strings.Contains(got, "second question") {
		t.Fatalf("RenderContext included item beyond MaxItems: %q", got)
	}
}

func TestRenderContext_EmptyResultsReturnsEmptyString(t *testing.T) {
	if got := RenderContext(nil, ContextOptions{}); got != "" {
		t.Fatalf("RenderContext(nil) = %q, want empty string", got)
	}
}
