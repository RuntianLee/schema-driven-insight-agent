// framework/eval_harness/runners/capture_test.go
package runners

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/RuntianLee/schema-driven-insight-agent/agent"
	"github.com/RuntianLee/schema-driven-insight-agent/contract"
)

func TestCaptureStoreRecordsToolCallAndFinalize(t *testing.T) {
	var store agent.TrajectoryStore = newCaptureStore()
	resp := contract.Response{Status: contract.StatusOK,
		Data: []contract.BucketRow{{Bucket: "20", PlayerCount: 150}}}
	store.RecordToolCall("query_distribution",
		map[string]any{"table": "player_basics"}, resp,
		time.Now(), time.Now(), nil)
	_ = store.Finalize(context.Background(), "success", "最终洞察", "")

	cs := store.(*captureStore)
	if len(cs.toolCalls) != 1 {
		t.Fatalf("want 1 tool call, got %d", len(cs.toolCalls))
	}
	tc := cs.toolCalls[0]
	if tc.Name != "query_distribution" || tc.Response.Status != contract.StatusOK {
		t.Fatalf("unexpected tool call: %+v", tc)
	}
	if tc.Args["table"] != "player_basics" {
		t.Fatalf("tool args not captured: %+v", tc.Args)
	}
	if tc.Response.Data[0].PlayerCount != 150 {
		t.Fatalf("response data not captured: %+v", tc.Response)
	}
	if cs.outcome != "success" || cs.finalOut != "最终洞察" {
		t.Fatalf("finalize not captured: outcome=%q final=%q", cs.outcome, cs.finalOut)
	}
}

func TestCaptureStoreNonResponseOutputRecordsErr(t *testing.T) {
	store := newCaptureStore()
	store.RecordToolCall("weird", nil, "not a Response", time.Now(), time.Now(), nil)
	if store.toolCalls[0].Err == nil {
		t.Fatalf("non-Response output should record a type-assert error (R2)")
	}
}

func TestCaptureStorePropagatesDispatchErr(t *testing.T) {
	store := newCaptureStore()
	store.RecordToolCall("query_distribution", nil, contract.Response{}, time.Now(), time.Now(), errors.New("boom"))
	if store.toolCalls[0].Err == nil {
		t.Fatalf("dispatch err should be preserved")
	}
}
