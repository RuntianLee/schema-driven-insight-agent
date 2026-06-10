// Package seed 生成确定性合成数据写入 toygame 的 Layer2 SQLite。
// 纯合成、不连任何生产库——开源仓库开箱即跑的样例数据。
package seed

import (
	"fmt"
	"os"

	fetl "github.com/RuntianLee/schema-driven-insight-agent/etl"
	"github.com/RuntianLee/schema-driven-insight-agent/schema_protocol"
)

// hashSalt：toygame 示例静态盐（仅示例，非真实 PII）。
const hashSalt = "toygame_demo_v0"

// coinBuckets 定义确定性 coins 分布（player_count, perPlayerCoins）——
// 固定计数使 eval data_correctness 可断言精确值。
var coinBuckets = []struct {
	count int
	coins int64
}{
	{600, 50},    // 0~100
	{300, 500},   // 101~1k
	{80, 5000},   // 1k~1w
	{20, 50000},  // 1w+
}

// TotalPlayers 为各桶计数之和（确定性）。
var TotalPlayers = func() int64 {
	var t int64
	for _, b := range coinBuckets {
		t += int64(b.count)
	}
	return t
}()

// Seed 解析 schema → 生成确定性 basics + currencies 行 → 复用 framework/etl
// 的 LoadBasics/LoadCurrencies 写 Layer2（LoadCurrencies 顺带写 _meta.schema_version）。
// 返回写入玩家数。
func Seed(dbPath, schemaPath string) (int64, error) {
	raw, err := os.ReadFile(schemaPath)
	if err != nil {
		return 0, fmt.Errorf("read schema: %w", err)
	}
	s, err := schema_protocol.Parse(raw)
	if err != nil {
		return 0, fmt.Errorf("parse schema: %w", err)
	}
	cols, err := fetl.BasicsColumns(s, "player_basics")
	if err != nil {
		return 0, fmt.Errorf("basics columns: %w", err)
	}

	colIdx := make(map[string]int, len(cols))
	for i, c := range cols {
		colIdx[c.Name] = i
	}
	const lastLogin int64 = 1700000000 // 固定时间戳（确定性）
	var basics [][]any
	var currencies []fetl.CurrencyRow
	seq := int64(1)
	for _, b := range coinBuckets {
		for i := 0; i < b.count; i++ {
			level := int64(1 + (seq % 30)) // 1~30 级确定性分布
			row := make([]any, len(cols))
			row[colIdx["coins"]] = b.coins
			row[colIdx["last_login"]] = lastLogin
			row[colIdx["level"]] = level
			basics = append(basics, row)
			currencies = append(currencies, fetl.CurrencyRow{
				PlayerID:     fetl.HashPID(hashSalt, seq),
				CurrencyType: "coins",
				Balance:      b.coins,
			})
			seq++
		}
	}

	if err := fetl.LoadBasics(basics, cols, "player_basics", dbPath, []string{"level"}); err != nil {
		return 0, fmt.Errorf("load basics: %w", err)
	}
	if err := fetl.LoadCurrencies(currencies, "player_currencies", dbPath, s.Version); err != nil {
		return 0, fmt.Errorf("load currencies: %w", err)
	}
	return int64(len(basics)), nil
}
