package evaluators

import (
	"context"
	"testing"
	"time"

	"github.com/RuntianLee/schema-driven-insight-agent/llm"
	"github.com/RuntianLee/schema-driven-insight-agent/trajectory"
)

// spyRec 实现 LLMTokenRecorder，记录入参。
type spyRec struct {
	called bool
	role   string
	tout   int
}

func (s *spyRec) RecordLLMCallRole(role, _, _, _ string, _, tout int, _ float64, _, _ time.Time, _ error) {
	s.called = true
	s.role = role
	s.tout = tout
}

func TestRecordingClient_RecordsWhenCtxHasRecorder(t *testing.T) {
	rec := &spyRec{}
	ctx := ContextWithRecorder(context.Background(), rec)
	c := NewRecordingClient(constJudge(`["a"]`), "judge:claim_coverage")
	resp, _, _, _, err := c.Call(ctx, "prompt")
	if err != nil || resp != `["a"]` {
		t.Fatalf("透传应原样: resp=%q err=%v", resp, err)
	}
	if !rec.called || rec.role != "judge:claim_coverage" {
		t.Fatalf("应记录到 role=judge:claim_coverage，got called=%v role=%q", rec.called, rec.role)
	}
}

func TestRecordingClient_NoRecorderInCtx_PassThrough(t *testing.T) {
	c := NewRecordingClient(constJudge(`["a"]`), "judge:x")
	resp, _, _, _, err := c.Call(context.Background(), "prompt") // ctx 无 recorder
	if err != nil || resp != `["a"]` {
		t.Fatalf("无 recorder 应仅透传: resp=%q err=%v", resp, err)
	}
}

func TestRecordingClient_WithJudgeProfile_DelegatesAndKeepsRole(t *testing.T) {
	spy := &spyProfiler{Client: NewMockJudge()} // 复用 judge_profile_test.go 的 spyProfiler
	c := NewRecordingClient(spy, "judge:claim_coverage")
	out := c.(llm.JudgeProfiler).WithJudgeProfile(8000, true)
	if !spy.called || spy.gotMax != 8000 || !spy.gotDisable {
		t.Fatalf("应委托内层 WithJudgeProfile(8000,true): called=%v max=%d dis=%v", spy.called, spy.gotMax, spy.gotDisable)
	}
	if _, ok := out.(recordingClient); !ok {
		t.Fatalf("应仍是 recordingClient（记录壳保留），got %T", out)
	}
}

func TestRecordingClient_NonProfilerInner_PassThroughSelf(t *testing.T) {
	c := NewRecordingClient(constJudge("x"), "judge:x") // mockJudge 非 JudgeProfiler
	out := c.(llm.JudgeProfiler).WithJudgeProfile(8000, true)
	if _, ok := out.(recordingClient); !ok {
		t.Fatalf("非 profiler 内层应返回自身 recordingClient，got %T", out)
	}
}

func TestRecordingClient_EndToEnd_PersistsJudgeRole(t *testing.T) {
	db, err := trajectory.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := trajectory.Migrate(db); err != nil {
		t.Fatal(err)
	}
	rec, err := trajectory.New(context.Background(), db, "v", "q?", "benchmark")
	if err != nil {
		t.Fatal(err)
	}
	ctx := ContextWithRecorder(context.Background(), rec)
	c := NewRecordingClient(constJudge(`["主张"]`), "judge:claim_coverage")
	if _, _, _, _, err := c.Call(ctx, "prompt"); err != nil {
		t.Fatal(err)
	}
	if err := rec.Finalize(context.Background(), "success", "", ""); err != nil {
		t.Fatal(err)
	}
	var role string
	var tout int
	if err := db.QueryRow(`SELECT role, tokens_output FROM trajectory_steps WHERE step_type='llm_call'`).Scan(&role, &tout); err != nil {
		t.Fatalf("查 judge 步: %v", err)
	}
	if role != "judge:claim_coverage" {
		t.Fatalf("role = %q, want judge:claim_coverage", role)
	}
}
