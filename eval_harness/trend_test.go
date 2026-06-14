// framework/eval_harness/trend_test.go
package eval_harness

import (
	"strings"
	"testing"
)

func TestParseTrend_AggregatesPassPct(t *testing.T) {
	jsonl := strings.Join([]string{
		`{"commit":"c1","ran_at":100,"adapter":"b3","agent_version":"v0.1.0","task_id":"t1","evaluator":"data_correctness","pass":true,"value":1}`,
		`{"commit":"c1","ran_at":100,"adapter":"b3","agent_version":"v0.1.0","task_id":"t2","evaluator":"data_correctness","pass":false,"value":0}`,
		`{"commit":"c1","ran_at":100,"adapter":"b3","agent_version":"v0.1.0","task_id":"t1","evaluator":"reasoning_quality","pass":false,"value":0.6}`,
	}, "\n")
	pts, err := ParseTrend([]byte(jsonl))
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) != 1 {
		t.Fatalf("应聚合成 1 个点，得 %d", len(pts))
	}
	if pts[0].PassPct != 50 {
		t.Fatalf("passPct=%g want 50", pts[0].PassPct)
	}
}

func TestRenderTrendHTML_SelfContained(t *testing.T) {
	pts := []TrendPoint{{AgentVersion: "v0.1.0", Adapter: "b3", PassPct: 50, FirstRanAt: 100}}
	html := RenderTrendHTML(pts, nil)
	if !strings.Contains(html, "<svg") || !strings.Contains(html, "</html>") {
		t.Fatalf("应是含内联 SVG 的完整 HTML")
	}
	if strings.Contains(html, "http://") || strings.Contains(html, "https://") || strings.Contains(html, "cdn") {
		t.Fatalf("应零外部依赖（无 http/cdn 引用）")
	}
}
