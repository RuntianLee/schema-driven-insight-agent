package memory

import (
	"strings"
	"testing"
)

func TestScrubText_ReusesTrajectoryRedaction(t *testing.T) {
	input := "玩家 123456789 with hash abcdef1234567890 ran SELECT * FROM t WHERE note = 'this_is_a_very_long_secret_literal_value'"

	got := ScrubText(input)

	for _, leaked := range []string{
		"123456789",
		"abcdef1234567890",
		"this_is_a_very_long_secret_literal_value",
	} {
		if strings.Contains(got, leaked) {
			t.Fatalf("ScrubText leaked %q in %q", leaked, got)
		}
	}
}

func TestScrubItem_ScrubsTextFieldsOnly(t *testing.T) {
	item := Item{
		Question:      "玩家 123456789 question",
		Summary:       "hash abcdef1234567890 summary",
		AnswerOutline: "WHERE note = 'this_is_a_very_long_secret_literal_value'",
		Tools:         []string{"abcdef1234567890"},
		Tags:          []string{"123456789"},
	}

	got := ScrubItem(item)

	for name, value := range map[string]string{
		"Question":      got.Question,
		"Summary":       got.Summary,
		"AnswerOutline": got.AnswerOutline,
	} {
		if strings.Contains(value, "123456789") ||
			strings.Contains(value, "abcdef1234567890") ||
			strings.Contains(value, "this_is_a_very_long_secret_literal_value") {
			t.Fatalf("%s was not scrubbed: %q", name, value)
		}
	}
	if got.Tools[0] != item.Tools[0] {
		t.Fatalf("Tools must not be scrubbed: got %q want %q", got.Tools[0], item.Tools[0])
	}
	if got.Tags[0] != item.Tags[0] {
		t.Fatalf("Tags must not be scrubbed: got %q want %q", got.Tags[0], item.Tags[0])
	}
}
