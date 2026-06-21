// trajcapture/capture_test.go
package trajcapture

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/RuntianLee/schema-driven-insight-agent/contract"
)

func TestCaptureToolCallsAndAnalystResults(t *testing.T) {
	c := New()
	resp := contract.Response{Status: contract.StatusOK}
	c.RecordToolCall("analyze", map[string]any{"table": "customers"}, resp, time.Now(), time.Now(), nil)
	c.RecordToolCall("query_distribution", map[string]any{"column": "Exited"}, resp, time.Now(), time.Now(), nil)
	_ = c.Finalize(context.Background(), "success", "最终答案", "")

	if got := len(c.ToolCalls()); got != 2 {
		t.Fatalf("ToolCalls len=%d want 2", got)
	}
	if c.Outcome() != "success" || c.FinalOutput() != "最终答案" {
		t.Errorf("outcome=%q final=%q", c.Outcome(), c.FinalOutput())
	}
	rs := c.AnalystResults()
	if len(rs) != 2 || rs[0].ID != "q1" || rs[1].ID != "q2" {
		t.Fatalf("AnalystResults=%+v", rs)
	}
	if rs[0].Call.Name != "analyze" {
		t.Errorf("rs[0].Call.Name=%q", rs[0].Call.Name)
	}
}

// TestRecordToolCall_NonResponseOutput 验证 output 非 contract.Response（类型不匹配）时，
// Err 必须被设为非 nil（类型断言失败路径）。
func TestRecordToolCall_NonResponseOutput(t *testing.T) {
	c := New()
	c.RecordToolCall("weird", nil, "not a Response", time.Now(), time.Now(), nil)
	tcs := c.ToolCalls()
	if len(tcs) != 1 {
		t.Fatalf("ToolCalls len=%d want 1", len(tcs))
	}
	if tcs[0].Err == nil {
		t.Fatal("output 非 contract.Response 时 ToolCall.Err 应非 nil，实为 nil")
	}
}

// TestRecordToolCall_DispatchErr 验证调用方传入非 nil dispatch err 时，
// 该 err 被原样保留在 ToolCall.Err（dispatch 错误优先路径）。
func TestRecordToolCall_DispatchErr(t *testing.T) {
	c := New()
	boom := errors.New("dispatch: timeout")
	c.RecordToolCall("query_distribution", nil, contract.Response{}, time.Now(), time.Now(), boom)
	tcs := c.ToolCalls()
	if len(tcs) != 1 {
		t.Fatalf("ToolCalls len=%d want 1", len(tcs))
	}
	if tcs[0].Err == nil {
		t.Fatal("dispatch err 应被保留在 ToolCall.Err，实为 nil")
	}
	if !errors.Is(tcs[0].Err, boom) {
		t.Errorf("ToolCall.Err=%v，want errors.Is(..., boom)", tcs[0].Err)
	}
}
