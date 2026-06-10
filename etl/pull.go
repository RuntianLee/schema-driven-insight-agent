package etl

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/RuntianLee/schema-driven-insight-agent/etl_health"
	"github.com/RuntianLee/schema-driven-insight-agent/schema_protocol"
)

// BasicsOptions 驱动 state 表（如 player_basics）的物化。
type BasicsOptions struct {
	PGDSN      string
	SQLitePath string
	SchemaPath string
	Table      string
	MinRows    int
	IndexCols  []string
}

// PullBasics：读 schema 推导列 → extract → 行数闸门 → load。
// 不写 health / 不戳 _meta（由 PullCurrencies 末尾统一反映两表就绪）。
func PullBasics(ctx context.Context, o BasicsOptions) error {
	if err := os.MkdirAll(filepath.Dir(o.SQLitePath), 0o755); err != nil {
		return err
	}
	raw, err := os.ReadFile(o.SchemaPath)
	if err != nil {
		return fmt.Errorf("read schema %s: %w", o.SchemaPath, err)
	}
	s, err := schema_protocol.Parse(raw)
	if err != nil {
		return fmt.Errorf("parse schema: %w", err)
	}
	cols, err := BasicsColumns(s, o.Table)
	if err != nil {
		return err
	}
	sc, err := ParseScope(raw)
	if err != nil {
		return err
	}
	where, args := sc.WhereClause()
	rows, err := ExtractBasics(ctx, o.PGDSN, o.Table, colNames(cols), where, args)
	if err != nil {
		return err
	}
	if err := ValidateRowCount(len(rows), o.MinRows); err != nil {
		return err
	}
	return LoadBasics(rows, cols, o.Table, o.SQLitePath, o.IndexCols)
}

// CurrenciesOptions 驱动派生 player_currencies 的 pivot + health。
type CurrenciesOptions struct {
	PGDSN         string
	SQLitePath    string
	HealthPath    string
	SrcTable      string
	DestTable     string
	PIDCol        string
	Salt          string
	MinRows       int
	SchemaVersion int
	Money         []MoneyColumn

	// 以下为可选扩展；无 scope 时 → 缺省值 → 行为/health JSON 逐字不变。
	SchemaPath      string // 非空则解析 scope 过滤 currencies 源（如按 server 维度圈定）
	Frozen          bool   // 冻结快照标记 → 写入 health
	MinRowsOverride *int64 // adapter 自定 Ready 行数阈值 → 写入 health
	DataAsOf        int64  // 快照"有效现在"（unix 秒）→ 写入 health
}

// PullCurrencies：extract→闸门→load→writeHealth→备份轮转（best-effort）。
func PullCurrencies(ctx context.Context, o CurrenciesOptions) error {
	if err := os.MkdirAll(filepath.Dir(o.SQLitePath), 0o755); err != nil {
		return err
	}
	var where string
	var args []any
	if o.SchemaPath != "" {
		raw, err := os.ReadFile(o.SchemaPath)
		if err != nil {
			writeFailedHealth(o.HealthPath, o.SchemaVersion, err)
			return fmt.Errorf("read schema %s: %w", o.SchemaPath, err)
		}
		sc, err := ParseScope(raw)
		if err != nil {
			writeFailedHealth(o.HealthPath, o.SchemaVersion, err)
			return err
		}
		where, args = sc.WhereClause()
	}
	rows, err := ExtractCurrencies(ctx, o.PGDSN, o.SrcTable, o.PIDCol, o.Salt, o.Money, where, args)
	if err != nil {
		writeFailedHealth(o.HealthPath, o.SchemaVersion, err)
		return err
	}
	if err := ValidateRowCount(len(rows), o.MinRows); err != nil {
		writeFailedHealth(o.HealthPath, o.SchemaVersion, err)
		return err
	}
	if err := LoadCurrencies(rows, o.DestTable, o.SQLitePath, o.SchemaVersion); err != nil {
		writeFailedHealth(o.HealthPath, o.SchemaVersion, err)
		return err
	}
	if err := etl_health.Write(o.HealthPath, etl_health.Health{
		Status:          etl_health.StatusOK,
		Rows:            int64(len(rows)),
		FinishedAt:      time.Now(),
		SchemaVersion:   o.SchemaVersion,
		Frozen:          o.Frozen,
		MinRowsOverride: o.MinRowsOverride,
		DataAsOf:        o.DataAsOf,
	}); err != nil {
		return err
	}
	if err := RotateBackup(o.SQLitePath); err != nil {
		log.Printf("warn: backup rotation: %v", err)
	}
	return nil
}

func writeFailedHealth(path string, schemaVersion int, cause error) {
	if err := etl_health.Write(path, etl_health.Health{
		Status:        etl_health.StatusFailed,
		FinishedAt:    time.Now(),
		SchemaVersion: schemaVersion,
		Reason:        cause.Error(),
	}); err != nil {
		log.Printf("warn: writeFailedHealth: %v", err)
	}
}
