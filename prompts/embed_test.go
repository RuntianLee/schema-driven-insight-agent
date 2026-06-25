package prompts

import (
	"strings"
	"testing"
)

func TestSystemPromptHasNoBaselineNumbers(t *testing.T) {
	// design-v3 §4 #4：system prompt 不预投 baseline 数字。
	for _, forbidden := range []string{"21.56", "1.58", "63.41", "266871", "359079"} {
		if strings.Contains(SystemV0, forbidden) {
			t.Fatalf("system prompt must NOT contain baseline number %q", forbidden)
		}
	}
	if !strings.Contains(SystemV0, "query_distribution") {
		t.Fatal("prompt must document the tool")
	}
}

func TestSystemPromptDocumentsCountWithoutPIIColumn(t *testing.T) {
	for _, want := range []string{
		`{"fn":"count","as":"n"}`,
		"不要写",
		"player_id",
		"PII",
	} {
		if !strings.Contains(SystemV0, want) {
			t.Fatalf("system prompt must document count-without-PII rule; missing %q", want)
		}
	}
}

func TestAdvisorPromptNonEmptyAndNoBaselineNumbers(t *testing.T) {
	// Guard: ensure advisor_v0.md is embedded and not accidentally emptied.
	if strings.TrimSpace(AdvisorV0) == "" {
		t.Fatal("AdvisorV0 must not be empty")
	}
	// design-v3 §4 #4：advisor prompt 同样不预投 baseline 数字（与 SystemV0 共用同一禁止列表）。
	for _, forbidden := range []string{"21.56", "1.58", "63.41", "266871", "359079"} {
		if strings.Contains(AdvisorV0, forbidden) {
			t.Fatalf("advisor prompt must NOT contain baseline number %q", forbidden)
		}
	}
}

func TestSystemPromptDocumentsAnalyzeAggregateWhitelist(t *testing.T) {
	for _, want := range []string{
		"analyze 不支持",
		"stddev",
		"median",
		"percentile",
		"query_distribution",
		"profile.stddev",
	} {
		if !strings.Contains(SystemV0, want) {
			t.Fatalf("system prompt must document analyze aggregate whitelist; missing %q", want)
		}
	}
}

func TestSystemPromptDocumentsLiteralArrayAnchor(t *testing.T) {
	// 锚文法适应：prompt 须把字面 JSON-path 形式（数组下标）作为示例，与 resolver navSelector 对齐。
	for _, want := range []string{
		"groups[i]",
		"q2.groups[1].data[0].avg_value",
	} {
		if !strings.Contains(SystemV0, want) {
			t.Fatalf("system prompt 须文档化字面数组下标锚；缺 %q", want)
		}
	}
}

func TestSystemPromptDerivedCallFormAndOneAnchor(t *testing.T) {
	// T1 实证驱动：派生式须用函数调用形式（非中缀 ratio=a/b 被抄）、表寻址复数首选、一锚一主张。
	for _, want := range []string{
		"ratio(a,b)",            // 派生式函数调用形式（非中缀 ratio=）
		"table.rows[i]",         // 表寻址复数 rows 首选（镜像字面 JSON）
		"一个数字主张对应一条锚", // 一锚一主张纪律（禁逗号塞多单元格）
	} {
		if !strings.Contains(SystemV0, want) {
			t.Fatalf("system prompt 缺 %q（T1 锚文法补全）", want)
		}
	}
}
