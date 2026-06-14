// framework/eval_harness/abhistory.go
// A/B 增益的 PII-free 聚合历史 writer：独立于 eval-history.jsonl（守 real-llm-eval-lane D2，
// 真 LLM 道不污染确定性趋势）。仅写聚合白名单列，供趋势 HTML 第二面板消费。
package eval_harness

import (
	"encoding/json"
	"fmt"
	"os"
)

// ABHistoryMeta 是一次 A/B 运行的 PII-free 元数据。
type ABHistoryMeta struct {
	Commit       string
	Adapter      string
	AgentVersion string
	RanAt        int64
}

type abHistoryRow struct {
	Commit        string  `json:"commit"`
	RanAt         int64   `json:"ran_at"`
	Adapter       string  `json:"adapter"`
	AgentVersion  string  `json:"agent_version"`
	Runs          int     `json:"runs"`
	MeanPassRateA float64 `json:"mean_pass_rate_a"`
	MeanPassRateB float64 `json:"mean_pass_rate_b"`
	MeanDelta     float64 `json:"mean_delta"`
	Meets20Pct    bool    `json:"meets_20pct"`
}

// AppendABHistoryJSONL 追加一行 A/B 聚合摘要（O_APPEND|O_CREATE）。仅聚合列，无任务/prompt 明细。
func AppendABHistoryJSONL(path string, ab *ABReport, meta ABHistoryMeta) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open ab-history %s: %w", path, err)
	}
	defer f.Close()
	line, err := json.Marshal(abHistoryRow{
		Commit: meta.Commit, RanAt: meta.RanAt, Adapter: meta.Adapter,
		AgentVersion: meta.AgentVersion, Runs: ab.Runs,
		MeanPassRateA: ab.MeanPassRateA, MeanPassRateB: ab.MeanPassRateB,
		MeanDelta: ab.MeanDelta, Meets20Pct: ab.Meets20Pct,
	})
	if err != nil {
		return fmt.Errorf("marshal ab-history: %w", err)
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("write ab-history: %w", err)
	}
	return nil
}
