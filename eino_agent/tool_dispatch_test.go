package eino_agent

import (
	"context"
	"testing"

	"github.com/RuntianLee/schema-driven-insight-agent/contract"
)

func TestRecordedDispatch_FiresToolCallback(t *testing.T) {
	rec := &fakeRecorder{}
	ctx := withTrajectory(context.Background(), rec, "MiniMax-M2.7")
	disp := &stubDispatcher{resp: map[string]contract.Response{"analyze": {Status: contract.StatusOK}}}
	resp, err := recordedDispatch(ctx, disp, "analyze", map[string]any{"table": "t"})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if resp.Status != contract.StatusOK {
		t.Fatalf("status=%v", resp.Status)
	}
	if len(rec.toolCalls) != 1 || rec.toolCalls[0] != "analyze" {
		t.Fatalf("RecordToolCall not fired via callback: %v", rec.toolCalls)
	}
}
