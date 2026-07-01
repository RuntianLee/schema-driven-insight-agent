package eino_agent

import (
	"strings"
	"testing"
	"time"
)

func TestBuildSystemPrompt_NoQuestionEmbedded(t *testing.T) {
	sys := buildSystemPrompt("SYSTEM", "SCHEMA", time.Unix(1700000000, 0))
	if !strings.Contains(sys, "SYSTEM") || !strings.Contains(sys, "SCHEMA") {
		t.Fatal("missing system/schema")
	}
	if strings.Contains(sys, "## 运营问题") {
		t.Fatal("system prompt 不应含运营问题段（question 走 UserMessage）")
	}
	if !strings.Contains(sys, "cutoff") {
		t.Fatal("missing cutoff 速查表")
	}
}
