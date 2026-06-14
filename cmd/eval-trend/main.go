// Command eval-trend 从 eval-history.jsonl 渲染零依赖静态趋势 HTML（可选附 ab-history 第二面板）。
package main

import (
	"flag"
	"fmt"
	"os"

	evalpkg "github.com/RuntianLee/schema-driven-insight-agent/eval_harness"
)

func main() {
	hist := flag.String("history", "eval-history.jsonl", "eval-history JSONL 路径")
	abHist := flag.String("ab-history", "", "ab-history JSONL 路径（可选第二面板）")
	out := flag.String("out", "trend.html", "输出 HTML 路径")
	flag.Parse()
	if err := run(*hist, *abHist, *out); err != nil {
		fmt.Fprintln(os.Stderr, "eval-trend 失败:", err)
		os.Exit(1)
	}
	fmt.Println("趋势 HTML →", *out)
}

func run(histPath, abHistPath, outPath string) error {
	histData, err := os.ReadFile(histPath)
	if err != nil {
		return fmt.Errorf("read history %s: %w", histPath, err)
	}
	points, err := evalpkg.ParseTrend(histData)
	if err != nil {
		return err
	}
	var abPoints []evalpkg.ABTrendPoint
	if abHistPath != "" {
		abData, err2 := os.ReadFile(abHistPath)
		if err2 == nil {
			abPoints, err2 = evalpkg.ParseABTrend(abData)
			if err2 != nil {
				return err2
			}
		}
	}
	html := evalpkg.RenderTrendHTML(points, abPoints)
	return os.WriteFile(outPath, []byte(html), 0o644)
}
