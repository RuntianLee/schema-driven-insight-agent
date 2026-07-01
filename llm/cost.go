package llm

// CostUSD 按 MiniMax 计价（minimax.go 的 costPerKToken* 常量）从 token 数算美元成本。
// 迁移后 Eino ChatModel 只回 token usage、不回美元，Agent 腿预算闸经此换算。
func CostUSD(tokIn, tokOut int) float64 {
	return float64(tokIn)/1000*costPerKTokenIn + float64(tokOut)/1000*costPerKTokenOut
}
