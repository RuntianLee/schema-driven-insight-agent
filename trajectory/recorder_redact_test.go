package trajectory

import (
	"context"
	"strings"
	"testing"
	"time"
)

// input_question / final_output 与 step 同等待遇：写入前脱敏（README「PII redacted on write」）。
func TestRedactOnWrite_QuestionAndFinalOutput(t *testing.T) {
	db := openMigrated(t)
	ctx := context.Background()

	const hash = "deadbeef01234567" // 16 位 hex，命中 rePlayerHash
	rec, err := New(ctx, db, "v0.1.0", "玩家 "+hash+" 的余额分布?", "production")
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if err := rec.Finalize(ctx, "success", "玩家 "+hash+" 余额报告", ""); err != nil {
		t.Fatalf("finalize: %v", err)
	}

	var question, finalOutput string
	err = db.QueryRow(`SELECT input_question, final_output FROM trajectories WHERE trajectory_id=?`,
		rec.TrajectoryID()).Scan(&question, &finalOutput)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	for name, got := range map[string]string{"input_question": question, "final_output": finalOutput} {
		if strings.Contains(got, hash) {
			t.Errorf("%s 未脱敏: %q", name, got)
		}
		if !strings.Contains(got, "<player>") {
			t.Errorf("%s 缺脱敏占位符: %q", name, got)
		}
	}
}

// Finalize 后到达的 Record 调用必须丢弃而非 panic（导出 API 健壮性）。
func TestRecordAfterFinalize_NoPanic(t *testing.T) {
	db := openMigrated(t)
	ctx := context.Background()
	rec, err := New(ctx, db, "v0.1.0", "q", "benchmark")
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if err := rec.Finalize(ctx, "success", "out", ""); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	now := time.Now()
	rec.RecordToolCall("query_distribution", nil, nil, now, now, nil) // 不得 panic
	rec.RecordLLMCall("p", "r", "m", 1, 1, 0, now, now, nil)          // 不得 panic
	rec.RecordReasoning("thought", now, now)                          // 不得 panic
}
