package prompts

import (
	"strings"
	"testing"
)

func TestSystemPromptHasNoBaselineNumbers(t *testing.T) {
	// design-v3 §4 #4：system prompt 不预投 baseline 数字。
	for _, forbidden := range []string{"21.56", "1.58", "63.41", "266871", "359079"} {
		if strings.Contains(SystemV0, forbidden) {
			t.Fatalf("system prompt must NOT contain baseline number %q", forbidden)
		}
	}
	if !strings.Contains(SystemV0, "query_distribution") {
		t.Fatal("prompt must document the tool")
	}
}
