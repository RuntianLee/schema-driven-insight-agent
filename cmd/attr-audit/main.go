// Command attr-audit 离线回溯：扫 trajectory DB，量化有多少 GATE「实得0条」其实是本 parse 假失败
// （旧 float64 严解=0 条但新 ClaimedNumber 解=≥1 条），并统计 claimed_value 里倍率词频次（探针条款）。
// 零 LLM、零网络。用法：attr-audit [-resolve] <dir-or-glob-of-*.db>...
//
// -resolve 模式：对每个含归因块的 trajectory，从 trajectory_steps 按 step_index 顺序
// 重建 []contract.ToolCall（tool_call 步的 output 字段即 contract.Response JSON），
// 再对每条 claim 跑 EvalAnchor，统计 resolved/mismatch/unresolvable。
package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/RuntianLee/schema-driven-insight-agent/contract"
	"github.com/RuntianLee/schema-driven-insight-agent/eval_harness/evaluators"
	"github.com/RuntianLee/schema-driven-insight-agent/eval_harness/runners"
	_ "modernc.org/sqlite"
)

// toolCallSignatures 是四大格式家族「未被执行的工具调用」在最终答案里的特征标记。
// 修复后 agent 的工具调用应成为 tool_call step、不再出现在 final_output；
// 若仍出现，说明该格式形态被解析器漏掉（泄漏）。顺序=优先报告更具体的外层壳。
var toolCallSignatures = []string{"<minimax:tool_call>", "<invoke", "<tool_call>", "[TOOL_CALLS]"}

// detectToolCallLeak 检测一段最终答案是否是「未执行的工具调用泄漏」，返回命中的签名。
func detectToolCallLeak(finalOutput string) (string, bool) {
	for _, sig := range toolCallSignatures {
		if strings.Contains(finalOutput, sig) {
			return sig, true
		}
	}
	return "", false
}

// oldStrictParse 复刻修复前 float64 严解的 all-or-nothing 行为，返回解出的 claim 数（解码失败=0）。
func oldStrictParse(raw string) int {
	const needle = `{"attribution":`
	idx := strings.Index(raw, needle)
	if idx < 0 {
		return 0
	}
	var out struct {
		Attribution []struct {
			ClaimedValue float64 `json:"claimed_value"`
		} `json:"attribution"`
	}
	if err := json.NewDecoder(strings.NewReader(raw[idx:])).Decode(&out); err != nil {
		return 0
	}
	return len(out.Attribution)
}

// multiplierRe 精确匹配 claimed_value 字符串里"数字紧跟倍率词"（如 "20万"/"1.2亿"/"3k"），
// 而非整段输出里任意出现的字母——避免 k/M/B 单字母 ASCII 在英文/JSON 文本里的假阳性。
var multiplierRe = regexp.MustCompile(`"claimed_value"\s*:\s*"[0-9.,]+\s*(万|千|亿|k|K|M|B)`)

// loadToolCalls 从 trajectory_steps 表按 step_index 顺序读 tool_call 步，
// 将每步的 output 字段（contract.Response JSON）unmarshal 并组成 []contract.ToolCall。
// 顺序与原始 trajectory 一致，保证 q{N} 锚的 0-based 编号正确对应。
func loadToolCalls(db *sql.DB, trajectoryID string) ([]contract.ToolCall, error) {
	rows, err := db.Query(
		`SELECT step_index, coalesce(tool_name,''), coalesce(output,'') FROM trajectory_steps
		 WHERE trajectory_id=? AND step_type='tool_call'
		 ORDER BY step_index`,
		trajectoryID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var calls []contract.ToolCall
	for rows.Next() {
		var stepIndex int
		var name, output string
		if err := rows.Scan(&stepIndex, &name, &output); err != nil {
			return nil, err
		}
		var resp contract.Response
		if output != "" {
			if err := json.Unmarshal([]byte(output), &resp); err != nil {
				// output 不是 Response JSON（坏行/别的形状）→ 跳过会让 OKCalls 编号错位，
				// 必须告警（带 trajectory_id + step_index + err）而非静默吞掉。
				fmt.Fprintf(os.Stderr, "warn loadToolCalls %s step_index=%d: unmarshal Response: %v\n",
					trajectoryID, stepIndex, err)
				continue
			}
		}
		calls = append(calls, contract.ToolCall{Name: name, Response: resp})
	}
	return calls, rows.Err()
}

// resolveStats 是单个 trajectory 的 EvalAnchor 统计结果。
type resolveStats struct {
	resolved    int
	mismatch    int
	unresolvable int
	total       int
}

func (s resolveStats) String() string {
	pct := 0.0
	if s.total > 0 {
		pct = float64(s.resolved) / float64(s.total) * 100
	}
	return fmt.Sprintf("total=%d resolved=%d(%.0f%%) mismatch=%d unresolvable=%d",
		s.total, s.resolved, pct, s.mismatch, s.unresolvable)
}

func main() {
	resolveMode := flag.Bool("resolve", false, "对每条归因块 claim 跑 EvalAnchor，统计 resolved/mismatch/unresolvable")
	xmlLeakMode := flag.Bool("xmlleak", false, "扫 final_output 标记未执行工具调用泄漏（四家族签名）")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "用法: attr-audit [-resolve] [-xmlleak] <*.db | dir>...")
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(2)
	}

	var dbPaths []string
	for _, arg := range flag.Args() {
		info, err := os.Stat(arg)
		if err == nil && info.IsDir() {
			matches, _ := filepath.Glob(filepath.Join(arg, "*.db"))
			dbPaths = append(dbPaths, matches...)
		} else {
			dbPaths = append(dbPaths, arg)
		}
	}

	var total, hadBlock, recovered, multiplierHits, leakHits int

	// -resolve 模式的全局计数
	var resTotal, resResolved, resMismatch, resUnresolvable int

	for _, p := range dbPaths {
		db, err := sql.Open("sqlite", p)
		if err != nil {
			fmt.Fprintf(os.Stderr, "skip %s: %v\n", p, err)
			continue
		}
		rows, err := db.Query(`SELECT trajectory_id, coalesce(final_output,'') FROM trajectories`)
		if err != nil {
			db.Close()
			fmt.Fprintf(os.Stderr, "skip %s: %v\n", p, err)
			continue
		}
		for rows.Next() {
			var id, fo string
			if err := rows.Scan(&id, &fo); err != nil {
				fmt.Fprintf(os.Stderr, "warn %s: row scan error: %v\n", p, err)
				continue
			}
			total++
			if *xmlLeakMode {
				if sig, leaked := detectToolCallLeak(fo); leaked {
					leakHits++
					fmt.Printf("XMLLEAK %s::%s  sig=%q\n", filepath.Base(p), id, sig)
				}
			}
			if !strings.Contains(fo, `{"attribution":`) {
				continue
			}
			hadBlock++
			oldN := oldStrictParse(fo)
			newClaims, _ := runners.ParseAttributionOutput(fo)
			newN := len(newClaims)
			isRecovered := oldN == 0 && newN > 0
			if isRecovered {
				recovered++
				fmt.Printf("RECOVERED %s::%s  old=0 new=%d\n", filepath.Base(p), id, newN)
			}
			if multiplierRe.MatchString(fo) {
				multiplierHits++
			}

			// -resolve 模式：对当前 trajectory 重建 calls 并逐 claim 跑 EvalAnchor
			if *resolveMode && newN > 0 {
				calls, err := loadToolCalls(db, id)
				if err != nil {
					fmt.Fprintf(os.Stderr, "warn %s::%s load tool calls: %v\n", filepath.Base(p), id, err)
					continue
				}
				var stats resolveStats
				for _, c := range newClaims {
					v := evaluators.EvalAnchor(calls, c.Anchor, float64(c.ClaimedValue), 0.01)
					stats.total++
					switch v.Status {
					case evaluators.AttrResolved:
						stats.resolved++
					case evaluators.AttrMismatch:
						stats.mismatch++
					default:
						// unresolvable / derived_unsupported 均计入 unresolvable 桶
						stats.unresolvable++
					}
				}
				resTotal += stats.total
				resResolved += stats.resolved
				resMismatch += stats.mismatch
				resUnresolvable += stats.unresolvable

				// 对 recovered run 额外打印逐 run 结果
				if isRecovered {
					fmt.Printf("  └─ RESOLVE %s::%s  %s\n", filepath.Base(p), id, stats)
				}
			}
		}
		if err := rows.Err(); err != nil {
			fmt.Fprintf(os.Stderr, "warn %s: rows iteration error: %v\n", p, err)
		}
		rows.Close()
		db.Close()
	}

	fmt.Printf("\n=== 离线回溯汇总 ===\n")
	fmt.Printf("trajectories 总数: %d\n", total)
	fmt.Printf("含归因块: %d\n", hadBlock)
	fmt.Printf("parse 假失败可恢复（旧0/新≥1）: %d\n", recovered)
	fmt.Printf("倍率词探针命中（claimed_value 字符串里数字紧跟 万/亿/千/k/M/B）: %d  —— 若材料性>0 触发倍率缩放档（spec §6）\n", multiplierHits)

	if *xmlLeakMode {
		fmt.Printf("未执行工具调用泄漏（final_output 含四家族签名）: %d / %d trajectories\n", leakHits, total)
	}

	if *resolveMode {
		pct := 0.0
		if resTotal > 0 {
			pct = float64(resResolved) / float64(resTotal) * 100
		}
		fmt.Printf("\n=== -resolve 值解析层汇总（所有含归因块 trajectory）===\n")
		fmt.Printf("claims 总数: %d\n", resTotal)
		fmt.Printf("resolved: %d (%.1f%%)\n", resResolved, pct)
		fmt.Printf("mismatch: %d\n", resMismatch)
		fmt.Printf("unresolvable: %d\n", resUnresolvable)
	}
}
