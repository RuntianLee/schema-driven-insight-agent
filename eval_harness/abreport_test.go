// framework/eval_harness/abreport_test.go
package eval_harness

import (
	"testing"

	"github.com/RuntianLee/schema-driven-insight-agent/eval_harness/evaluators"
)

// repWith 构造一个单任务 Report，data_correctness pass 由 pass 控制。
func repWith(taskID string, pass bool) *Report {
	r := NewReport([]string{"data_correctness", "reasoning_quality"})
	r.Add(taskID, evaluators.Score{Evaluator: "data_correctness", Value: b2f(pass), Pass: pass}, true)
	r.Add(taskID, evaluators.Score{Evaluator: "reasoning_quality", Value: 0.6, Pass: false}, false)
	return r
}

// repWithJudge 构造一个单任务 Report，reasoning_quality Value 可自定（data_correctness 固定通过）。
func repWithJudge(taskID string, judgeVal float64) *Report {
	r := NewReport([]string{"data_correctness", "reasoning_quality"})
	r.Add(taskID, evaluators.Score{Evaluator: "data_correctness", Value: 1, Pass: true}, true)
	r.Add(taskID, evaluators.Score{Evaluator: "reasoning_quality", Value: judgeVal, Pass: judgeVal >= 0.6}, false)
	return r
}
func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func TestBuildABReport_DeltaAndMeets20(t *testing.T) {
	a := []*Report{repWith("t1", false), repWith("t1", false), repWith("t1", false), repWith("t1", false)}
	b := []*Report{repWith("t1", true), repWith("t1", true), repWith("t1", true), repWith("t1", true)}
	ab, err := BuildABReport("baseline", "reflection", 4, a, b)
	if err != nil {
		t.Fatal(err)
	}
	if ab.MeanPassRateA != 0 || ab.MeanPassRateB != 1 {
		t.Fatalf("passRate A=%g B=%g want 0/1", ab.MeanPassRateA, ab.MeanPassRateB)
	}
	if ab.MeanDelta != 1.0 || !ab.Meets20Pct {
		t.Fatalf("delta=%g meets20=%v want 1.0/true", ab.MeanDelta, ab.Meets20Pct)
	}
	if len(ab.Tasks) != 1 || ab.Tasks[0].PassRateB != 1 {
		t.Fatalf("per-task 聚合错: %+v", ab.Tasks)
	}
}

func TestBuildABReport_OverlapSetsCaveat(t *testing.T) {
	a := []*Report{repWith("t1", true), repWith("t1", true), repWith("t1", false), repWith("t1", false)}
	b := []*Report{repWith("t1", true), repWith("t1", true), repWith("t1", true), repWith("t1", false)}
	ab, err := BuildABReport("baseline", "reflection", 4, a, b)
	if err != nil {
		t.Fatal(err)
	}
	if ab.Caveat == "" {
		t.Fatalf("区间重叠应填 Caveat")
	}
}

func TestBuildABReport_SingleRun_BNotAboveA_SetsCaveat(t *testing.T) {
	// runs=1：B 通过率不高于 A（此处 A pass、B pass，rate 相等）→ 单次无法判定显著 → Caveat。
	ab, err := BuildABReport("baseline", "reflection", 1,
		[]*Report{repWith("t1", true)}, []*Report{repWith("t1", true)})
	if err != nil {
		t.Fatal(err)
	}
	if ab.Caveat == "" {
		t.Fatalf("runs=1 且 B≤A 应填 Caveat（样本不足）")
	}
}

func TestBuildABReport_SingleRun_BAboveA_NoCaveat(t *testing.T) {
	// runs=1：B 通过（rate 1）> A 失败（rate 0）→ B 最低 > A 最高 → 不触发 Caveat。
	ab, err := BuildABReport("baseline", "reflection", 1,
		[]*Report{repWith("t1", false)}, []*Report{repWith("t1", true)})
	if err != nil {
		t.Fatal(err)
	}
	if ab.Caveat != "" {
		t.Fatalf("runs=1 且 B>A 不应填 Caveat，得 %q", ab.Caveat)
	}
}

func TestBuildABReport_LenMismatch(t *testing.T) {
	if _, err := BuildABReport("a", "b", 2, []*Report{repWith("t1", true)}, nil); err == nil {
		t.Fatal("runs 与报告数不符应报错")
	}
}

// TestBuildABReport_JudgeDelta 验证 MeanJudgeDelta 与 Meets20PctJudge 正确聚合：
// config B 的 reasoning_quality Value 比 A 高 ≥0.20，应触发 Meets20PctJudge=true。
func TestBuildABReport_JudgeDelta(t *testing.T) {
	// A: judgeVal=0.40；B: judgeVal=0.80 → delta=0.40 ≥ 0.20
	aReports := []*Report{repWithJudge("t1", 0.40), repWithJudge("t1", 0.40)}
	bReports := []*Report{repWithJudge("t1", 0.80), repWithJudge("t1", 0.80)}
	ab, err := BuildABReport("baseline", "reflection", 2, aReports, bReports)
	if err != nil {
		t.Fatal(err)
	}
	if ab.MeanJudgeA != 0.40 {
		t.Fatalf("MeanJudgeA want 0.40, got %g", ab.MeanJudgeA)
	}
	if ab.MeanJudgeB != 0.80 {
		t.Fatalf("MeanJudgeB want 0.80, got %g", ab.MeanJudgeB)
	}
	if ab.MeanJudgeDelta <= 0 {
		t.Fatalf("MeanJudgeDelta should be >0, got %g", ab.MeanJudgeDelta)
	}
	if !ab.Meets20PctJudge {
		t.Fatalf("Meets20PctJudge should be true for delta=%g", ab.MeanJudgeDelta)
	}
}
