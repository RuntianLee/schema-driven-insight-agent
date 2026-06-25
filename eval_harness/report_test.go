// framework/eval_harness/report_test.go
package eval_harness

import (
	"strings"
	"testing"

	"github.com/RuntianLee/schema-driven-insight-agent/eval_harness/evaluators"
)

func TestReportGateFailedOnDeterministicFail(t *testing.T) {
	r := NewReport([]string{"data_correctness"})
	r.Add("t1", evaluators.Score{Evaluator: "data_correctness", Value: 0, Pass: false}, true)
	if !r.GateFailed() {
		t.Fatalf("deterministic fail must fail gate")
	}
}

func TestReportGateIgnoresJudge(t *testing.T) {
	r := NewReport([]string{"reasoning_quality"})
	r.Add("t1", evaluators.Score{Evaluator: "reasoning_quality", Value: 0.6, Pass: false}, false)
	if r.GateFailed() {
		t.Fatalf("judge (non-deterministic) must not affect gate")
	}
}

func TestReportConsoleTableContainsTaskAndGate(t *testing.T) {
	r := NewReport([]string{"data_correctness"})
	r.Add("stage_distribution", evaluators.Score{Evaluator: "data_correctness", Value: 1, Pass: true, Display: "1.00 ✓"}, true)
	out := r.ConsoleTable()
	if !strings.Contains(out, "stage_distribution") || !strings.Contains(out, "GATE") {
		t.Fatalf("console table missing content:\n%s", out)
	}
}

func TestReportConsoleTableSurfacesFailureDetail(t *testing.T) {
	// gate 失败/异常的 Detail（如 attribution badDetails、claim_coverage 错误）必须可见，
	// 否则只见 "0/5 ✗" 无法定位哪条锚为何挂。
	r := NewReport([]string{"attribution_grounding", "claim_coverage", "reasoning_quality"})
	r.Add("t1", evaluators.Score{
		Evaluator: "attribution_grounding", Value: 0, Pass: false,
		Display: "0/1 ✗ (1 mismatch/unresolvable)",
		Detail:  `「EU ARPU 是 US 的 2 倍」anchor="q2.groups[1].avg" unresolvable`,
	}, true)
	// 通过的确定性项不该印明细。
	r.Add("t1", evaluators.Score{Evaluator: "claim_coverage", Pass: false, Display: "1.00", Detail: ""}, false)
	// 非失败、无 Detail 的项不该印。
	r.Add("t1", evaluators.Score{Evaluator: "reasoning_quality", Pass: true, Display: "5/5", Detail: "judge reason 略"}, false)
	out := r.ConsoleTable()
	if !strings.Contains(out, `anchor="q2.groups[1].avg"`) || !strings.Contains(out, "unresolvable") {
		t.Fatalf("console table 须暴露 attribution 失败明细:\n%s", out)
	}
	if !strings.Contains(out, "attribution_grounding") {
		t.Fatalf("明细须标注 evaluator 名:\n%s", out)
	}
	// 通过的 judge（reasoning_quality, Pass=true）不该把它的 Detail 印进明细块。
	if strings.Contains(out, "judge reason 略") {
		t.Fatalf("通过项不应渲染 Detail:\n%s", out)
	}
}

func TestReportMarkdownAndJSON(t *testing.T) {
	r := NewReport([]string{"data_correctness"})
	r.Add("t1", evaluators.Score{Evaluator: "data_correctness", Value: 1, Pass: true, Display: "1.00 ✓"}, true)
	if !strings.Contains(r.Markdown(), "| t1 |") {
		t.Fatalf("markdown missing row")
	}
	js, err := r.JSON()
	if err != nil || !strings.Contains(string(js), "data_correctness") {
		t.Fatalf("json bad: %v", err)
	}
}
