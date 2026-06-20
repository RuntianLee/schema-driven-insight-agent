// trajcapture/tee_test.go
package trajcapture

import (
	"context"
	"testing"
	"time"

	"github.com/RuntianLee/schema-driven-insight-agent/contract"
)

// fakeRec 是最小 agent.TrajectoryStore，记录是否被双扇到。
type fakeRec struct {
	tools     int
	llm       int
	reasoning int
	finalized bool
}

func (f *fakeRec) TrajectoryID() string { return "rec-1" }
func (f *fakeRec) RecordLLMCall(_, _, _ string, _, _ int, _ float64, _, _ time.Time, _ error) {
	f.llm++
}
func (f *fakeRec) RecordToolCall(_ string, _, _ any, _, _ time.Time, _ error) { f.tools++ }
func (f *fakeRec) RecordReasoning(_ string, _, _ time.Time)                   { f.reasoning++ }
func (f *fakeRec) Finalize(_ context.Context, _, _, _ string) error           { f.finalized = true; return nil }

func TestTeeFansToBoth(t *testing.T) {
	cap := New()
	rec := &fakeRec{}
	tee := NewTee(cap, rec)
	if tee.TrajectoryID() != "rec-1" {
		t.Errorf("TrajectoryID=%q want rec-1（取持久化侧）", tee.TrajectoryID())
	}
	tee.RecordToolCall("analyze", map[string]any{}, contract.Response{Status: contract.StatusOK}, time.Now(), time.Now(), nil)
	_ = tee.Finalize(context.Background(), "success", "ans", "")
	if rec.tools != 1 || !rec.finalized {
		t.Errorf("rec 未被双扇: tools=%d finalized=%v", rec.tools, rec.finalized)
	}
	if len(cap.ToolCalls()) != 1 || cap.Outcome() != "success" {
		t.Errorf("cap 未被双扇: tools=%d outcome=%q", len(cap.ToolCalls()), cap.Outcome())
	}
}

// TestTeeFansLLMCallToBoth 验证 Tee.RecordLLMCall 同时扇给 cap（内存侧）和 rec（持久化侧）。
func TestTeeFansLLMCallToBoth(t *testing.T) {
	cap := New()
	rec := &fakeRec{}
	tee := NewTee(cap, rec)
	now := time.Now()
	tee.RecordLLMCall("prompt-text", "response-text", "model-x", 100, 50, 0.001, now, now, nil)
	if rec.llm != 1 {
		t.Errorf("rec.llm=%d want 1：RecordLLMCall 未扇到持久化侧", rec.llm)
	}
	// cap.RecordLLMCall 是空操作，不落数据；只验证调用不 panic 即可（已由 rec 侧覆盖扇出）。
}

// TestTeeFansReasoningToBoth 验证 Tee.RecordReasoning 同时扇给 cap 和 rec。
func TestTeeFansReasoningToBoth(t *testing.T) {
	cap := New()
	rec := &fakeRec{}
	tee := NewTee(cap, rec)
	now := time.Now()
	tee.RecordReasoning("I think …", now, now)
	if rec.reasoning != 1 {
		t.Errorf("rec.reasoning=%d want 1：RecordReasoning 未扇到持久化侧", rec.reasoning)
	}
	// cap.RecordReasoning 是空操作；同上只验证不 panic。
}
