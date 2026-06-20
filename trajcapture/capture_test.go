// trajcapture/capture_test.go
package trajcapture

import (
	"context"
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
