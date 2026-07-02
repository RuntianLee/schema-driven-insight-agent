package trajectory

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func openMigrated(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "traj.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestRecorderWriteReadRoundTrip(t *testing.T) {
	db := openMigrated(t)
	ctx := context.Background()

	rec, err := New(ctx, db, "v0.1.0", "虚拟货币分布?", "production")
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	now := time.Now()
	rec.RecordLLMCall("prompt", "resp", "minimax-m2.7", 10, 20, 0.001, now, now.Add(time.Millisecond), nil)
	rec.RecordToolCall("query_distribution", map[string]any{"currency_type": "virtual"},
		[]any{map[string]any{"bucket": "0~1w"}}, now, now.Add(time.Millisecond), errors.New("boom"))
	if err := rec.Finalize(ctx, "success", "final report", ""); err != nil {
		t.Fatalf("finalize: %v", err)
	}

	var outcome string
	var stepCount, totalTokens int
	err = db.QueryRow(`SELECT outcome, step_count, total_tokens FROM trajectories WHERE trajectory_id=?`,
		rec.TrajectoryID()).Scan(&outcome, &stepCount, &totalTokens)
	if err != nil {
		t.Fatalf("query trajectory: %v", err)
	}
	if outcome != "success" || stepCount != 2 || totalTokens != 30 {
		t.Fatalf("summary mismatch: outcome=%s steps=%d tokens=%d", outcome, stepCount, totalTokens)
	}

	var nSteps int
	db.QueryRow(`SELECT COUNT(*) FROM trajectory_steps WHERE trajectory_id=?`, rec.TrajectoryID()).Scan(&nSteps)
	if nSteps != 2 {
		t.Fatalf("expected 2 step rows, got %d", nSteps)
	}

	var toolErr string
	db.QueryRow(`SELECT error FROM trajectory_steps WHERE step_type='tool_call' AND trajectory_id=?`,
		rec.TrajectoryID()).Scan(&toolErr)
	if toolErr != "boom" {
		t.Fatalf("expected tool error 'boom', got %q", toolErr)
	}

	var taskClass string
	db.QueryRow(`SELECT task_class FROM trajectories WHERE trajectory_id=?`, rec.TrajectoryID()).Scan(&taskClass)
	if taskClass != "production" {
		t.Errorf("task_class = %q, want production", taskClass)
	}
}

func TestFinalizeIdempotent(t *testing.T) {
	db := openMigrated(t)
	ctx := context.Background()
	rec, _ := New(ctx, db, "v0.1.0", "q", "benchmark")
	if err := rec.Finalize(ctx, "success", "out", ""); err != nil {
		t.Fatalf("first finalize: %v", err)
	}
	if err := rec.Finalize(ctx, "abort", "", "double"); err != nil {
		t.Fatalf("second finalize must not panic: %v", err)
	}
}

// TestPersistStepFailure_CountedInErrorSummary 锁 M-5：persistStep 写入失败（如表被删/只读库）
// 须计数并在 Finalize 时并入 error_summary（对齐 dropped 计数器的既有呈现方式），
// 而不是像此前那样静默吞掉、零可观测性。
func TestPersistStepFailure_CountedInErrorSummary(t *testing.T) {
	db := openMigrated(t)
	ctx := context.Background()
	rec, err := New(ctx, db, "v0.1.0", "q", "benchmark")
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	// 制造 persistStep 必然失败的条件：trajectory_steps 表在 New 之后被删，
	// trajectories 表仍在（Finalize 的 UPDATE 仍能成功，从而能读到 error_summary）。
	if _, err := db.Exec(`DROP TABLE trajectory_steps`); err != nil {
		t.Fatalf("drop trajectory_steps: %v", err)
	}
	now := time.Now()
	rec.RecordToolCall("query_distribution", nil, nil, now, now, nil)
	rec.RecordLLMCall("p", "r", "m", 1, 1, 0, now, now, nil)

	if err := rec.Finalize(ctx, "success", "out", ""); err != nil {
		t.Fatalf("finalize: %v", err)
	}

	var errSummary sql.NullString
	if err := db.QueryRow(`SELECT error_summary FROM trajectories WHERE trajectory_id=?`,
		rec.TrajectoryID()).Scan(&errSummary); err != nil {
		t.Fatalf("query error_summary: %v", err)
	}
	if !errSummary.Valid || !strings.Contains(errSummary.String, "write-failed") {
		t.Fatalf("error_summary 应包含写入失败计数信号，got=%q", errSummary.String)
	}
}

func TestRecordLLMCallRole_PersistsRoleAndExcludesFromTotal(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	rec, err := New(context.Background(), db, "v-test", "q?", "benchmark")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	rec.RecordLLMCall("p-agent", "r-agent", "m", 100, 200, 0.001, now, now, nil) // role=agent
	rec.RecordLLMCallRole("judge:claim_coverage", "p-j", "r-j", "m", 10, 20, 0.002, now, now, nil)
	if err := rec.Finalize(context.Background(), "success", "out", ""); err != nil {
		t.Fatal(err)
	}

	// per-step role 落库正确
	var agentRole, judgeRole string
	db.QueryRow(`SELECT role FROM trajectory_steps WHERE step_type='llm_call' AND tokens_input=100`).Scan(&agentRole)
	db.QueryRow(`SELECT role FROM trajectory_steps WHERE step_type='llm_call' AND tokens_input=10`).Scan(&judgeRole)
	if agentRole != "agent" {
		t.Fatalf("agent step role = %q, want agent", agentRole)
	}
	if judgeRole != "judge:claim_coverage" {
		t.Fatalf("judge step role = %q, want judge:claim_coverage", judgeRole)
	}

	// total_tokens 汇总 = agent-only（100+200=300），不含 judge（10+20）
	var total int
	db.QueryRow(`SELECT total_tokens FROM trajectories WHERE trajectory_id=?`, rec.TrajectoryID()).Scan(&total)
	if total != 300 {
		t.Fatalf("total_tokens = %d, want 300（agent-only，排除 judge）", total)
	}
}
