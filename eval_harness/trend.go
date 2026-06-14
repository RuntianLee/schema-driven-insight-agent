// framework/eval_harness/trend.go
// 跨版本趋势可视化：从 eval-history.jsonl 聚合 data_correctness 通过率，渲染零依赖静态 HTML
// （内联 SVG，无服务器/无前端依赖）。可选第二面板读 ab-history.jsonl 的 reflection 增益。
package eval_harness

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// TrendPoint 是趋势主面板一个点：某 agent_version×adapter 的 data_correctness 通过率。
type TrendPoint struct {
	AgentVersion string
	Adapter      string
	PassPct      float64 // 0..100
	FirstRanAt   int64
}

// ABTrendPoint 是第二面板一个点：某 agent_version×adapter 的 reflection 增益。
type ABTrendPoint struct {
	AgentVersion string
	Adapter      string
	MeanDelta    float64
	FirstRanAt   int64
}

type histLine struct {
	RanAt        int64  `json:"ran_at"`
	Adapter      string `json:"adapter"`
	AgentVersion string `json:"agent_version"`
	Evaluator    string `json:"evaluator"`
	Pass         bool   `json:"pass"`
}

// ParseTrend 聚合 data_correctness 通过率（按 agent_version×adapter，按首次 ran_at 升序）。
func ParseTrend(jsonl []byte) ([]TrendPoint, error) {
	type acc struct {
		pass, total int
		firstRanAt  int64
		adapter     string
		version     string
	}
	groups := map[string]*acc{}
	sc := bufio.NewScanner(bytes.NewReader(jsonl))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var h histLine
		if err := json.Unmarshal([]byte(line), &h); err != nil {
			return nil, fmt.Errorf("parse trend line: %w", err)
		}
		if h.Evaluator != "data_correctness" {
			continue
		}
		key := h.AgentVersion + "\x00" + h.Adapter
		a := groups[key]
		if a == nil {
			a = &acc{firstRanAt: h.RanAt, adapter: h.Adapter, version: h.AgentVersion}
			groups[key] = a
		}
		a.total++
		if h.Pass {
			a.pass++
		}
		if h.RanAt < a.firstRanAt {
			a.firstRanAt = h.RanAt
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	var pts []TrendPoint
	for _, a := range groups {
		pct := 0.0
		if a.total > 0 {
			pct = float64(a.pass) * 100.0 / float64(a.total)
		}
		pts = append(pts, TrendPoint{AgentVersion: a.version, Adapter: a.adapter, PassPct: pct, FirstRanAt: a.firstRanAt})
	}
	sort.Slice(pts, func(i, j int) bool {
		if pts[i].FirstRanAt != pts[j].FirstRanAt {
			return pts[i].FirstRanAt < pts[j].FirstRanAt
		}
		return pts[i].Adapter < pts[j].Adapter
	})
	return pts, nil
}

// ParseABTrend 聚合 ab-history.jsonl 的 mean_delta（按 ran_at 升序）。
func ParseABTrend(jsonl []byte) ([]ABTrendPoint, error) {
	type abLine struct {
		RanAt        int64   `json:"ran_at"`
		Adapter      string  `json:"adapter"`
		AgentVersion string  `json:"agent_version"`
		MeanDelta    float64 `json:"mean_delta"`
	}
	var pts []ABTrendPoint
	sc := bufio.NewScanner(bytes.NewReader(jsonl))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var a abLine
		if err := json.Unmarshal([]byte(line), &a); err != nil {
			return nil, fmt.Errorf("parse ab-trend line: %w", err)
		}
		pts = append(pts, ABTrendPoint{AgentVersion: a.AgentVersion, Adapter: a.Adapter, MeanDelta: a.MeanDelta, FirstRanAt: a.RanAt})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	sort.Slice(pts, func(i, j int) bool { return pts[i].FirstRanAt < pts[j].FirstRanAt })
	return pts, nil
}

// RenderTrendHTML 渲染自包含 HTML（内联 SVG，零外部依赖）。abPoints 非空则附第二面板。
func RenderTrendHTML(points []TrendPoint, abPoints []ABTrendPoint) string {
	var b strings.Builder
	b.WriteString("<!DOCTYPE html>\n<html lang=\"zh\"><head><meta charset=\"utf-8\">")
	b.WriteString("<title>Eval 趋势</title><style>body{font-family:system-ui,sans-serif;margin:2rem;color:#1a1a1a}")
	b.WriteString("h2{margin-top:2rem}svg{border:1px solid #ddd;background:#fafafa}.lbl{font-size:11px;fill:#555}</style></head><body>")
	b.WriteString("<h1>Eval Harness 跨版本趋势</h1>")

	b.WriteString("<h2>data_correctness 通过率（按版本）</h2>")
	b.WriteString(renderLineSVG(trendSeries(points), 100, "%"))

	if len(abPoints) > 0 {
		b.WriteString("<h2>reflection 增益（mean_delta，A/B）</h2>")
		b.WriteString(renderLineSVG(abSeries(abPoints), 1.0, "Δ"))
	}
	b.WriteString("</body></html>")
	return b.String()
}

type seriesPoint struct {
	label string
	value float64
}

func trendSeries(pts []TrendPoint) []seriesPoint {
	out := make([]seriesPoint, len(pts))
	for i, p := range pts {
		out[i] = seriesPoint{label: p.AgentVersion + "/" + p.Adapter, value: p.PassPct}
	}
	return out
}

func abSeries(pts []ABTrendPoint) []seriesPoint {
	out := make([]seriesPoint, len(pts))
	for i, p := range pts {
		out[i] = seriesPoint{label: p.AgentVersion + "/" + p.Adapter, value: p.MeanDelta}
	}
	return out
}

// renderLineSVG 画一条折线 + 数据点 + x 轴标签。yMax 定纵轴上限，unit 仅用于值标签后缀。
func renderLineSVG(pts []seriesPoint, yMax float64, unit string) string {
	const w, h, pad = 720, 240, 40
	if len(pts) == 0 {
		return "<p>（暂无数据）</p>"
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<svg width="%d" height="%d" viewBox="0 0 %d %d">`, w, h, w, h)
	plotW, plotH := float64(w-2*pad), float64(h-2*pad)
	x := func(i int) float64 {
		if len(pts) == 1 {
			return float64(pad) + plotW/2
		}
		return float64(pad) + plotW*float64(i)/float64(len(pts)-1)
	}
	y := func(v float64) float64 {
		if yMax == 0 {
			return float64(h - pad)
		}
		return float64(pad) + plotH*(1-v/yMax)
	}
	// 折线：polyline points = "x1,y1 x2,y2 …"
	var poly strings.Builder
	for i, p := range pts {
		if i > 0 {
			poly.WriteByte(' ')
		}
		fmt.Fprintf(&poly, "%.1f,%.1f", x(i), y(p.value))
	}
	fmt.Fprintf(&b, `<polyline fill="none" stroke="#2563eb" stroke-width="2" points="%s"/>`, poly.String())
	for i, p := range pts {
		fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="3" fill="#2563eb"/>`, x(i), y(p.value))
		fmt.Fprintf(&b, `<text class="lbl" x="%.1f" y="%.1f" text-anchor="middle">%.1f%s</text>`,
			x(i), y(p.value)-8, p.value, unit)
		fmt.Fprintf(&b, `<text class="lbl" x="%.1f" y="%d" text-anchor="middle">%s</text>`,
			x(i), h-pad+16, escapeXML(p.label))
	}
	b.WriteString("</svg>")
	return b.String()
}

func escapeXML(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}
