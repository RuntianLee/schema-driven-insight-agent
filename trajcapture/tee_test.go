// trajcapture/tee_test.go
package trajcapture

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/RuntianLee/schema-driven-insight-agent/contract"
	"github.com/RuntianLee/schema-driven-insight-agent/trajectory"

	_ "modernc.org/sqlite"
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

// TestTeeWithRealSQLite 端到端验证 Tee.RecordToolCall 同时落库到真实 SQLite（恢复被删的
// eval_harness/runners.TestTeeStore_FansOutToCaptureAndSQLite 的等价覆盖）。
func TestTeeWithRealSQLite(t *testing.T) {
	ctx := context.Background()

	// 打开真实 SQLite 并建表。
	db, err := trajectory.Open(filepath.Join(t.TempDir(), "traj.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := trajectory.Migrate(db); err != nil {
		t.Fatal(err)
	}

	// 构造真实 Recorder，包在 Tee 里。
	rec, err := trajectory.New(ctx, db, "v0.1.0", "test-question", "benchmark")
	if err != nil {
		t.Fatal(err)
	}
	cap := New()
	tee := NewTee(cap, rec)

	// 调用一次 RecordToolCall，然后 Finalize。
	now := time.Now()
	tee.RecordToolCall(
		"query_distribution",
		map[string]any{"table": "player_basics"},
		contract.Response{Status: contract.StatusOK},
		now, now, nil,
	)
	if err := tee.Finalize(ctx, "success", "答案", ""); err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	// 内存侧：Capture 必须捕获到 1 条工具调用。
	if got := len(cap.ToolCalls()); got != 1 {
		t.Errorf("cap.ToolCalls() len = %d, want 1", got)
	}

	// SQLite 侧：trajectory_steps 应有 1 条 tool_call 类型的记录。
	var stepN int
	db.QueryRow(
		`SELECT count(*) FROM trajectory_steps WHERE trajectory_id=? AND step_type='tool_call'`,
		tee.TrajectoryID(),
	).Scan(&stepN)
	if stepN != 1 {
		t.Errorf("SQLite persisted tool_call steps = %d, want 1", stepN)
	}

	// TrajectoryID 必须来自持久化侧（rec），而非内存侧的固定字符串 "capture"。
	if tee.TrajectoryID() != rec.TrajectoryID() {
		t.Errorf("tee.TrajectoryID()=%q != rec.TrajectoryID()=%q", tee.TrajectoryID(), rec.TrajectoryID())
	}
}
