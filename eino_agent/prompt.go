package eino_agent

import (
	"fmt"
	"strings"
	"time"
)

// cutoffWindowsDays 是 prompt 里预算 cutoff 表覆盖的相对时间窗口。
// 模型自算 cutoff 偶有偏差（实测算成 9 天而非 3 天）；预算表让 Agent 抄、不要心算。
var cutoffWindowsDays = []int{1, 3, 7, 14, 30}

// buildSystemPrompt = system + schema 摘要 + 当前时间 + cutoff 速查表（不含 question；question 走 UserMessage）。
func buildSystemPrompt(systemPrompt, schemaContext string, now time.Time) string {
	var b strings.Builder
	b.WriteString(systemPrompt)
	if schemaContext != "" {
		b.WriteString("\n\n")
		b.WriteString(schemaContext)
	}
	nowUnix := now.Unix()
	fmt.Fprintf(&b, "\n\n## 当前时间\n今天是 %s（unix=%d）。\n", now.Format("2006-01-02"), nowUnix)
	b.WriteString("\n### 相对时间 cutoff 速查（“N 日未登录”/“N 日未活跃”等问题直接抄；不要心算）\n")
	for _, d := range cutoffWindowsDays {
		fmt.Fprintf(&b, "- %d 日：cutoff = %d\n", d, nowUnix-int64(d)*86400)
	}
	fmt.Fprintf(&b, "- 其他天数：cutoff = %d - N*86400（精确公式；上表覆盖外才用）\n", nowUnix)
	return b.String()
}
