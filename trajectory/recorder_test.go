package trajectory

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
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
