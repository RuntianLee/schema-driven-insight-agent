// Command attr-audit 离线回溯：扫 trajectory DB，量化有多少 GATE「实得0条」其实是本 parse 假失败
// （旧 float64 严解=0 条但新 ClaimedNumber 解=≥1 条），并统计 claimed_value 里倍率词频次（探针条款）。
// 零 LLM、零网络。用法：attr-audit <dir-or-glob-of-*.db>...
package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/RuntianLee/schema-driven-insight-agent/eval_harness/runners"
	_ "modernc.org/sqlite"
)

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

var multiplierWords = []string{"万", "千", "亿", "k", "K", "M", "B"}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "用法: attr-audit <*.db | dir>...")
		os.Exit(2)
	}
	var dbPaths []string
	for _, arg := range os.Args[1:] {
		info, err := os.Stat(arg)
		if err == nil && info.IsDir() {
			matches, _ := filepath.Glob(filepath.Join(arg, "*.db"))
			dbPaths = append(dbPaths, matches...)
		} else {
			dbPaths = append(dbPaths, arg)
		}
	}

	var total, hadBlock, recovered, multiplierHits int
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
				continue
			}
			total++
			if !strings.Contains(fo, `{"attribution":`) {
				continue
			}
			hadBlock++
			oldN := oldStrictParse(fo)
			newClaims, _ := runners.ParseAttributionOutput(fo)
			newN := len(newClaims)
			if oldN == 0 && newN > 0 {
				recovered++
				fmt.Printf("RECOVERED %s::%s  old=0 new=%d\n", filepath.Base(p), id, newN)
			}
			for _, w := range multiplierWords {
				if strings.Contains(fo, `claimed_value`) && strings.Contains(fo, w) {
					multiplierHits++
					break
				}
			}
		}
		rows.Close()
		db.Close()
	}

	fmt.Printf("\n=== 离线回溯汇总 ===\n")
	fmt.Printf("trajectories 总数: %d\n", total)
	fmt.Printf("含归因块: %d\n", hadBlock)
	fmt.Printf("parse 假失败可恢复（旧0/新≥1）: %d\n", recovered)
	fmt.Printf("倍率词探针命中（claimed 旁见 万/亿/k/M）: %d  —— 若材料性>0 触发倍率缩放档（spec §6）\n", multiplierHits)
}
