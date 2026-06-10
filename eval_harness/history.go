// framework/eval_harness/history.go
// PII-free verdict 摘要 writer：把一次 eval 运行的 Report 评分追加成 JSONL，供 CI
// 跨运行累积成版本历史（trajectory-spec-v2 §8）。只写白名单列，绝不触 prompt/
// final_output/player 数据。
package eval_harness

import (
	"encoding/json"
	"fmt"
	"os"
)

// HistoryMeta 是一次 eval 运行的 PII-free 元数据（由 cmd 提供）。
type HistoryMeta struct {
	Commit       string // git SHA（CI 传入；本地空）
	Adapter      string // e.g. "demo" / "myadapter"
	AgentVersion string // eino_agent.AgentVersion
	RanAt        int64  // Unix 秒
}

// historyRow 是 JSONL 的一行（白名单列）。
type historyRow struct {
	Commit       string  `json:"commit"`
	RanAt        int64   `json:"ran_at"`
	Adapter      string  `json:"adapter"`
	AgentVersion string  `json:"agent_version"`
	TaskID       string  `json:"task_id"`
	Evaluator    string  `json:"evaluator"`
	Pass         bool    `json:"pass"`
	Value        float64 `json:"value"`
}

// AppendHistoryJSONL 把 rep 里每个 (task, evaluator) 评分以 JSONL 追加到 path
// （O_APPEND|O_CREATE）。迭代顺序：rep.Tasks × rep.Evaluators（稳定、确定）。
func AppendHistoryJSONL(path string, rep *Report, meta HistoryMeta) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open history %s: %w", path, err)
	}
	defer f.Close()

	for _, taskID := range rep.Tasks {
		byEval := rep.Scores[taskID]
		for _, evalName := range rep.Evaluators {
			s, ok := byEval[evalName]
			if !ok {
				continue
			}
			line, err := json.Marshal(historyRow{
				Commit:       meta.Commit,
				RanAt:        meta.RanAt,
				Adapter:      meta.Adapter,
				AgentVersion: meta.AgentVersion,
				TaskID:       taskID,
				Evaluator:    evalName,
				Pass:         s.Pass,
				Value:        s.Value,
			})
			if err != nil {
				return fmt.Errorf("marshal history row: %w", err)
			}
			if _, err := f.Write(append(line, '\n')); err != nil {
				return fmt.Errorf("write history row: %w", err)
			}
		}
	}
	return nil
}
