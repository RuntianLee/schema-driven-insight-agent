package runners

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/RuntianLee/schema-driven-insight-agent/contract"
	"github.com/RuntianLee/schema-driven-insight-agent/trajectory"

	_ "modernc.org/sqlite"
)

func TestTeeStore_FansOutToCaptureAndSQLite(t *testing.T) {
	ctx := context.Background()
	db, err := trajectory.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := trajectory.Migrate(db); err != nil {
		t.Fatal(err)
	}

	cap := newCaptureStore()
	rec, err := trajectory.New(ctx, db, "v0.1.0", "q", "benchmark")
	if err != nil {
		t.Fatal(err)
	}
	tee := newTeeStore(cap, rec)

	now := time.Now()
	tee.RecordToolCall("query_distribution", map[string]any{"table": "player_basics"},
		contract.Response{Status: contract.StatusOK}, now, now, nil)
	if err := tee.Finalize(ctx, "success", "答案", ""); err != nil {
		t.Fatalf("finalize: %v", err)
	}

	// 内存侧（喂 evaluator）：toolCalls 捕获到
	if len(cap.toolCalls) != 1 {
		t.Errorf("captureStore.toolCalls = %d, want 1", len(cap.toolCalls))
	}
	// SQLite 侧：1 个 tool_call step 落库
	var stepN int
	db.QueryRow(`SELECT count(*) FROM trajectory_steps WHERE trajectory_id=? AND step_type='tool_call'`,
		tee.TrajectoryID()).Scan(&stepN)
	if stepN != 1 {
		t.Errorf("persisted tool_call steps = %d, want 1", stepN)
	}
	// TrajectoryID 来自持久化侧
	if tee.TrajectoryID() != rec.TrajectoryID() {
		t.Error("TrajectoryID should come from recorder")
	}
}
