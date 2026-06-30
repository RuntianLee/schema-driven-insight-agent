package evaluators

import (
	"context"
	"testing"

	"github.com/RuntianLee/schema-driven-insight-agent/llm"
)

// spyProfiler 实现 llm.JudgeProfiler，记录 WithJudgeProfile 入参；嵌入 mockJudge 满足 Call/Model。
type spyProfiler struct {
	llm.Client
	gotMax     int
	gotDisable bool
	called     bool
}

func (s *spyProfiler) WithJudgeProfile(maxTokens int, disableThinking bool) llm.Client {
	s.called = true
	s.gotMax = maxTokens
	s.gotDisable = disableThinking
	return s
}

func TestTuneJudge_ForwardsProfileToProfiler(t *testing.T) {
	spy := &spyProfiler{Client: NewMockJudge()}
	out := tuneJudge(spy, JudgeProfile{MaxTokens: 8000, DisableThinking: true})
	if !spy.called {
		t.Fatal("tuneJudge 未调用 WithJudgeProfile")
	}
	if spy.gotMax != 8000 || !spy.gotDisable {
		t.Fatalf("转发入参错: max=%d disable=%v", spy.gotMax, spy.gotDisable)
	}
	if out != spy {
		t.Fatal("应返回 profiler 的克隆结果")
	}
}

func TestTuneJudge_PassThroughNonProfiler(t *testing.T) {
	base := NewMockJudge()
	out := tuneJudge(base, JudgeProfile{MaxTokens: 8000, DisableThinking: true})
	if out != base {
		t.Fatal("非 profiler client 应原样透传")
	}
	if _, _, _, _, err := out.Call(context.Background(), "x"); err != nil {
		t.Fatalf("透传 client Call 报错: %v", err)
	}
}

func TestJudgeProfiles_Values(t *testing.T) {
	if claimCoverageProfile != (JudgeProfile{MaxTokens: judgeMaxTokens, DisableThinking: true}) {
		t.Fatalf("claimCoverageProfile 错: %+v", claimCoverageProfile)
	}
	if scoringJudgeProfile != (JudgeProfile{MaxTokens: judgeMaxTokens, DisableThinking: false}) {
		t.Fatalf("scoringJudgeProfile 错: %+v", scoringJudgeProfile)
	}
	if judgeMaxTokens != 8000 {
		t.Fatalf("judgeMaxTokens = %d, want 8000", judgeMaxTokens)
	}
}
