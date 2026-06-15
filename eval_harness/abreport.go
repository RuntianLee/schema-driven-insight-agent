// framework/eval_harness/abreport.go
// ABReport 是 reflection 增益 A/B 度量结果（真 LLM 道，off-gate）。纯聚合，与 report.go
// 同包（root），便于 BuildABReport 直接吃 []*Report，避免 runners↔eval_harness import 环。
package eval_harness

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ABTaskDelta 是单任务在 A/B 两配置下的对比。
type ABTaskDelta struct {
	TaskID    string  `json:"task_id"`
	PassRateA float64 `json:"pass_rate_a"` // data_correctness 通过率 = passes/runs
	PassRateB float64 `json:"pass_rate_b"`
	Delta     float64 `json:"delta"`   // B - A
	JudgeA    float64 `json:"judge_a"` // reasoning_quality 均值（次要；mock 道为占位）
	JudgeB    float64 `json:"judge_b"`
}

// ABReport 是 A/B 聚合结果。MinSuite/MaxSuite 是每轮 suite 级通过率的极差（方差信号）。
type ABReport struct {
	LabelA        string        `json:"label_a"`
	LabelB        string        `json:"label_b"`
	Runs          int           `json:"runs"`
	Tasks         []ABTaskDelta `json:"tasks"`
	MeanPassRateA float64       `json:"mean_pass_rate_a"`
	MeanPassRateB float64       `json:"mean_pass_rate_b"`
	MeanDelta     float64       `json:"mean_delta"`
	MinSuiteA     float64       `json:"min_suite_a"`
	MaxSuiteA     float64       `json:"max_suite_a"`
	MinSuiteB     float64       `json:"min_suite_b"`
	MaxSuiteB     float64       `json:"max_suite_b"`
	Meets20Pct      bool          `json:"meets_20pct"`
	MeanJudgeA      float64       `json:"mean_judge_a"`
	MeanJudgeB      float64       `json:"mean_judge_b"`
	MeanJudgeDelta  float64       `json:"mean_judge_delta"`
	Meets20PctJudge bool          `json:"meets_20pct_judge"`
	Caveat          string        `json:"caveat,omitempty"`
}

const dcEval = "data_correctness"
const rqEval = "reasoning_quality"

// BuildABReport 从两配置各 runs 次的 Report 聚合 A/B 增益。
// aReports/bReports 长度均须 == runs；任务集取自第一份 A 报告（稳定顺序）。
func BuildABReport(labelA, labelB string, runs int, aReports, bReports []*Report) (*ABReport, error) {
	if runs <= 0 {
		return nil, fmt.Errorf("runs 必须 > 0")
	}
	if len(aReports) != runs || len(bReports) != runs {
		return nil, fmt.Errorf("报告数与 runs 不符: A=%d B=%d runs=%d", len(aReports), len(bReports), runs)
	}
	taskIDs := append([]string(nil), aReports[0].Tasks...)

	ab := &ABReport{LabelA: labelA, LabelB: labelB, Runs: runs}
	var sumA, sumB float64
	for _, tid := range taskIDs {
		pa := passRateAcross(aReports, tid)
		pb := passRateAcross(bReports, tid)
		ab.Tasks = append(ab.Tasks, ABTaskDelta{
			TaskID: tid, PassRateA: pa, PassRateB: pb, Delta: pb - pa,
			JudgeA: judgeMeanAcross(aReports, tid), JudgeB: judgeMeanAcross(bReports, tid),
		})
		sumA += pa
		sumB += pb
	}
	n := float64(len(taskIDs))
	if n > 0 {
		ab.MeanPassRateA = sumA / n
		ab.MeanPassRateB = sumB / n
	}
	ab.MeanDelta = ab.MeanPassRateB - ab.MeanPassRateA
	ab.Meets20Pct = ab.MeanDelta >= 0.20

	var sumJA, sumJB float64
	for _, t := range ab.Tasks {
		sumJA += t.JudgeA
		sumJB += t.JudgeB
	}
	if n > 0 {
		ab.MeanJudgeA = sumJA / n
		ab.MeanJudgeB = sumJB / n
	}
	ab.MeanJudgeDelta = ab.MeanJudgeB - ab.MeanJudgeA
	ab.Meets20PctJudge = ab.MeanJudgeDelta >= 0.20

	ab.MinSuiteA, ab.MaxSuiteA = suiteRange(aReports, taskIDs)
	ab.MinSuiteB, ab.MaxSuiteB = suiteRange(bReports, taskIDs)
	// B 最低 <= A 最高 → delta 可能淹没在噪声里（runs=1 时即 B≤A，无"区间"概念），
	// 提示样本不足（spec B3.4 触发条件）。
	if ab.MinSuiteB <= ab.MaxSuiteA {
		ab.Caveat = "样本不足以判定增益显著（runs=1 即 B≤A，或多轮下 A/B 通过率区间重叠）：建议加大 -runs 或增加 headroom 难任务"
	}
	return ab, nil
}

// passRateAcross 统计某任务在多份报告里 data_correctness pass 的占比。
func passRateAcross(reps []*Report, taskID string) float64 {
	var pass int
	for _, r := range reps {
		if s, ok := r.Scores[taskID][dcEval]; ok && s.Pass {
			pass++
		}
	}
	return float64(pass) / float64(len(reps))
}

func judgeMeanAcross(reps []*Report, taskID string) float64 {
	var sum float64
	var cnt int
	for _, r := range reps {
		if s, ok := r.Scores[taskID][rqEval]; ok {
			sum += s.Value
			cnt++
		}
	}
	if cnt == 0 {
		return 0
	}
	return sum / float64(cnt)
}

// suiteRange 返回每轮 suite 级 data_correctness 通过率的 min/max。
func suiteRange(reps []*Report, taskIDs []string) (min, max float64) {
	if len(reps) == 0 || len(taskIDs) == 0 {
		return 0, 0
	}
	first := true
	for _, r := range reps {
		var pass int
		for _, tid := range taskIDs {
			if s, ok := r.Scores[tid][dcEval]; ok && s.Pass {
				pass++
			}
		}
		rate := float64(pass) / float64(len(taskIDs))
		if first || rate < min {
			min = rate
		}
		if first || rate > max {
			max = rate
		}
		first = false
	}
	return min, max
}

// ConsoleTable 渲染 A/B 摘要（演示）。
func (r *ABReport) ConsoleTable() string {
	var b strings.Builder
	fmt.Fprintf(&b, "A/B Eval — %s vs %s · runs=%d\n", r.LabelA, r.LabelB, r.Runs)
	b.WriteString("Task\tpass(A)\tpass(B)\tΔ\n")
	for _, t := range r.Tasks {
		fmt.Fprintf(&b, "%s\t%.2f\t%.2f\t%+.2f\n", t.TaskID, t.PassRateA, t.PassRateB, t.Delta)
	}
	fmt.Fprintf(&b, "MEAN\t%.2f\t%.2f\t%+.2f  (≥20%%: %v)\n",
		r.MeanPassRateA, r.MeanPassRateB, r.MeanDelta, r.Meets20Pct)
	fmt.Fprintf(&b, "MEAN judge(reasoning)\t%.2f\t%.2f\t%+.2f  (≥0.20: %v)\n",
		r.MeanJudgeA, r.MeanJudgeB, r.MeanJudgeDelta, r.Meets20PctJudge)
	if r.Caveat != "" {
		b.WriteString("CAVEAT: " + r.Caveat + "\n")
	}
	return b.String()
}

func (r *ABReport) JSON() ([]byte, error) { return json.MarshalIndent(r, "", "  ") }
