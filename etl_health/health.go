// Package etl_health 读写 ETL 健康文件，Agent 启动据此判断是否拒跑（design-v3 §13）。
package etl_health

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

const (
	StatusOK     = "OK"
	StatusFailed = "FAILED"
	MinRows      = 100000
	MaxStaleness = 24 * time.Hour
)

type Health struct {
	Status        string    `json:"status"`
	Rows          int64     `json:"rows"`
	FinishedAt    time.Time `json:"finished_at"`
	SchemaVersion int       `json:"schema_version"`
	Reason        string    `json:"reason,omitempty"`

	// 历史快照适配（缺省即线上 adapter 现状）：
	MinRowsOverride *int64 `json:"min_rows,omitempty"`   // 非空则取代包级 MinRows
	Frozen          bool   `json:"frozen,omitempty"`     // true 跳过 24h 失鲜检查
	DataAsOf        int64  `json:"data_as_of,omitempty"` // 快照"有效现在"(unix 秒)
}

func Write(path string, h Health) error {
	b, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return fmt.Errorf("etl_health marshal: %w", err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("etl_health write %s: %w", path, err)
	}
	return nil
}

func Read(path string) (Health, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Health{}, fmt.Errorf("etl_health read %s: %w", path, err)
	}
	var h Health
	if err := json.Unmarshal(b, &h); err != nil {
		return Health{}, fmt.Errorf("etl_health parse %s: %w", path, err)
	}
	return h, nil
}

// Ready：Status==OK && Rows>=阈值 && （非 frozen 时）新鲜度<MaxStaleness。
// 阈值取 MinRowsOverride（若有）否则包级 MinRows；frozen 快照跳失鲜（design-v3 §13 + 历史快照适配）。
func (h Health) Ready() (bool, string) {
	if h.Status != StatusOK {
		return false, fmt.Sprintf("etl status=%s reason=%s", h.Status, h.Reason)
	}
	minRows := int64(MinRows)
	if h.MinRowsOverride != nil {
		minRows = *h.MinRowsOverride
	}
	if h.Rows < minRows {
		return false, fmt.Sprintf("rows %d below threshold %d", h.Rows, minRows)
	}
	if !h.Frozen {
		if age := time.Since(h.FinishedAt); age > MaxStaleness {
			return false, fmt.Sprintf("etl stale: finished %s ago", age.Round(time.Minute))
		}
	}
	return true, ""
}
