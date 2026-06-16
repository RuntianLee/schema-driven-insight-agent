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

func TestSystemPromptDocumentsCountWithoutPIIColumn(t *testing.T) {
	for _, want := range []string{
		`{"fn":"count","as":"n"}`,
		"不要写",
		"player_id",
		"PII",
	} {
		if !strings.Contains(SystemV0, want) {
			t.Fatalf("system prompt must document count-without-PII rule; missing %q", want)
		}
	}
}

func TestSystemPromptDocumentsAnalyzeAggregateWhitelist(t *testing.T) {
	for _, want := range []string{
		"analyze 不支持",
		"stddev",
		"median",
		"percentile",
		"query_distribution",
		"profile.stddev",
	} {
		if !strings.Contains(SystemV0, want) {
			t.Fatalf("system prompt must document analyze aggregate whitelist; missing %q", want)
		}
	}
}
