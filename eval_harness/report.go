// framework/eval_harness/report.go
// report.go 属 Eval Harness 引擎根包（spec §6）。包注释见 doc.go。
package eval_harness

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/RuntianLee/schema-driven-insight-agent/eval_harness/evaluators"
)

// Report 是 N×M 评分矩阵。
type Report struct {
	Evaluators []string                               `json:"evaluators"`
	Tasks      []string                               `json:"tasks"`
	Scores     map[string]map[string]evaluators.Score `json:"scores"` // taskID → evalName → Score
	det        map[string]bool                        // evalName → Deterministic
}

func NewReport(evals []string) *Report {
	return &Report{
		Evaluators: evals,
		Scores:     map[string]map[string]evaluators.Score{},
		det:        map[string]bool{},
	}
}

// Add 记录一个 (task, evaluator) 评分；deterministic 标志决定是否参与 gate。
func (r *Report) Add(taskID string, s evaluators.Score, deterministic bool) {
	if _, ok := r.Scores[taskID]; !ok {
		r.Scores[taskID] = map[string]evaluators.Score{}
		r.Tasks = append(r.Tasks, taskID)
	}
	r.Scores[taskID][s.Evaluator] = s
	r.det[s.Evaluator] = deterministic
}

// GateFailed：任一 Deterministic evaluator 的 Pass==false。
func (r *Report) GateFailed() bool {
	for _, byEval := range r.Scores {
		for name, s := range byEval {
			if r.det[name] && !s.Pass {
				return true
			}
		}
	}
	return false
}

func (r *Report) sortedTasks() []string {
	out := append([]string(nil), r.Tasks...)
	sort.Strings(out)
	return out
}

// ConsoleTable 渲染演示用摘要表（含 GATE 行）。
func (r *Report) ConsoleTable() string {
	var b strings.Builder
	b.WriteString("Eval Suite — " + strings.Join(r.Evaluators, " · ") + "\n")
	header := "Task"
	for _, e := range r.Evaluators {
		header += "\t" + e
	}
	b.WriteString(header + "\n")
	for _, tid := range r.sortedTasks() {
		line := tid
		for _, e := range r.Evaluators {
			line += "\t" + r.Scores[tid][e].Display
		}
		b.WriteString(line + "\n")
	}
	gate := "PASS ✓"
	if r.GateFailed() {
		gate = "FAIL ✗"
	}
	b.WriteString("GATE (data_correctness): " + gate + "\n")
	return b.String()
}

// Markdown 渲染落盘表格（简历/博客素材）。
func (r *Report) Markdown() string {
	var b strings.Builder
	b.WriteString("| Task | " + strings.Join(r.Evaluators, " | ") + " |\n")
	b.WriteString("|---" + strings.Repeat("|---", len(r.Evaluators)) + "|\n")
	for _, tid := range r.sortedTasks() {
		b.WriteString("| " + tid + " |")
		for _, e := range r.Evaluators {
			b.WriteString(" " + r.Scores[tid][e].Display + " |")
		}
		b.WriteString("\n")
	}
	gate := "PASS"
	if r.GateFailed() {
		gate = "FAIL"
	}
	fmt.Fprintf(&b, "\n**GATE (data_correctness): %s**\n", gate)
	return b.String()
}

func (r *Report) JSON() ([]byte, error) { return json.MarshalIndent(r, "", "  ") }
