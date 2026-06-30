package llm

import (
	"testing"
	"time"
)

func TestWithJudgeProfile_ClonesImmutably(t *testing.T) {
	base := newMiniMaxFull("k", "m", "http://x", 5*time.Second, 2000, nil, "anthropic").(*minimaxClient)
	judge := base.WithJudgeProfile(8000, true).(*minimaxClient)

	if base.maxTokens != 2000 || base.disableThinking {
		t.Fatalf("base 被改动: maxTokens=%d disableThinking=%v", base.maxTokens, base.disableThinking)
	}
	if judge.maxTokens != 8000 || !judge.disableThinking {
		t.Fatalf("克隆 profile 错: maxTokens=%d disableThinking=%v", judge.maxTokens, judge.disableThinking)
	}
	if judge.format != "anthropic" || judge.model != "m" {
		t.Fatalf("克隆丢失字段: format=%q model=%q", judge.format, judge.model)
	}
	var _ JudgeProfiler = base
}
