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
