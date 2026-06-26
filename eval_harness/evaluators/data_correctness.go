// framework/eval_harness/evaluators/data_correctness.go
package evaluators

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/RuntianLee/schema-driven-insight-agent/contract"
	"gopkg.in/yaml.v3"
)

const floatTol = 0.0001

type dcRow struct {
	Match  map[string]string  `yaml:"match"`
	Expect map[string]float64 `yaml:"expect"`
}

type dcGroup struct {
	Match   map[string]string  `yaml:"match"`
	Rows    []dcRow            `yaml:"rows"`
	Profile map[string]float64 `yaml:"profile"`
}

type dcTableRow struct {
	Match     map[string]string  `yaml:"match"`
	Expect    map[string]float64 `yaml:"expect"`     // 按列名（别名）断言：确定性 mock 道用
	ExpectPos map[int]float64    `yaml:"expect_pos"` // 按列绝对位置断言：真 LLM 道别名鲁棒（agent 自选 as 别名时仍可比对）
	ExpectAny []dcTableExpectAny `yaml:"expect_any"` // 候选列名任一命中即可，避免 count 等前置列造成列序误判
	SingleRow bool               `yaml:"single_row"` // 断言唯一行：聚合 shape 无区分列可 match 时用
}

type dcTableExpectAny struct {
	Columns []string `yaml:"columns"`
	Value   float64  `yaml:"value"`
}

type dcAltBlock struct {
	Table   []dcTableRow       `yaml:"table"`
	Rows    []dcRow            `yaml:"rows"`
	Groups  []dcGroup          `yaml:"groups"`
	Profile map[string]float64 `yaml:"profile"`
}

type dcSpec struct {
	Tool         string             `yaml:"tool"`
	ExpectStatus string             `yaml:"expect_status"`
	Profile      map[string]float64 `yaml:"profile"`
	Rows         []dcRow            `yaml:"rows"`
	Groups       []dcGroup          `yaml:"groups"`
	Table        []dcTableRow       `yaml:"table"`
	AnyOf        []dcAltBlock       `yaml:"any_of"`
}

// DataCorrectness 是确定性 evaluator：对真实 tool 返回逐字段比对（spec §4.2）。
type DataCorrectness struct{}

func NewDataCorrectness() *DataCorrectness { return &DataCorrectness{} }

func (d *DataCorrectness) Name() string        { return "data_correctness" }
func (d *DataCorrectness) Deterministic() bool { return true }

func (d *DataCorrectness) Evaluate(_ context.Context, res TaskResult, spec *yaml.Node) (Score, error) {
	var sp dcSpec
	if err := spec.Decode(&sp); err != nil {
		return Score{}, fmt.Errorf("decode data_correctness spec: %w", err)
	}
	responses := findToolResponses(res, sp.Tool)
	if len(responses) == 0 {
		return fail(d.Name(), fmt.Sprintf("未找到 tool %q 的调用", sp.Tool)), nil
	}

	var allFails []string
	for i, resp := range responses {
		fails := checkResponse(resp, sp)
		if len(fails) == 0 {
			return Score{Evaluator: d.Name(), Value: 1.0, Pass: true, Display: "1.00 ✓"}, nil
		}
		allFails = append(allFails, fmt.Sprintf("调用%d: %s", i+1, strings.Join(fails, "; ")))
	}

	return fail(d.Name(), strings.Join(allFails, " | ")), nil
}

func checkResponse(resp contract.Response, sp dcSpec) []string {
	var fails []string
	if sp.ExpectStatus != "" && string(resp.Status) != sp.ExpectStatus {
		fails = append(fails, fmt.Sprintf("status=%s want %s", resp.Status, sp.ExpectStatus))
	}
	if sp.Profile != nil {
		fails = append(fails, checkProfile(resp.Profile, sp.Profile)...)
	}
	if len(sp.AnyOf) > 0 {
		return append(fails, checkAnyOf(resp, sp.AnyOf)...)
	}
	for _, r := range sp.Rows {
		fails = append(fails, checkRows(resp.Data, r)...)
	}
	for _, g := range sp.Groups {
		fails = append(fails, checkGroup(resp.Groups, g)...)
	}
	for _, tr := range sp.Table {
		fails = append(fails, checkTable(resp.Table, tr)...)
	}
	return fails
}

// checkAnyOf 任一分支零 fail → 整体过（返回 nil）；全分支失败 → 渲染各分支明细。
func checkAnyOf(resp contract.Response, blocks []dcAltBlock) []string {
	var rendered []string
	for i, b := range blocks {
		bf := checkAltBlock(resp, b)
		if len(bf) == 0 {
			return nil
		}
		rendered = append(rendered, fmt.Sprintf("[分支%d: %s]", i+1, strings.Join(bf, "; ")))
	}
	return []string{"any_of 全分支未过: " + strings.Join(rendered, " | ")}
}

// checkAltBlock 复用既有 table/rows/groups/profile 检查（不含 tool/expect_status/any_of）。
func checkAltBlock(resp contract.Response, b dcAltBlock) []string {
	var fails []string
	if b.Profile != nil {
		fails = append(fails, checkProfile(resp.Profile, b.Profile)...)
	}
	for _, r := range b.Rows {
		fails = append(fails, checkRows(resp.Data, r)...)
	}
	for _, g := range b.Groups {
		fails = append(fails, checkGroup(resp.Groups, g)...)
	}
	for _, tr := range b.Table {
		fails = append(fails, checkTable(resp.Table, tr)...)
	}
	return fails
}

// findToolResponses 返回同名 tool 的所有无运行错误响应。真实轨迹里 agent 可能
// 先完成核心查询再补充查询，data_correctness 应允许任一有效响应满足断言。
func findToolResponses(res TaskResult, tool string) []contract.Response {
	var responses []contract.Response
	for i := range res.ToolCalls {
		tc := res.ToolCalls[i]
		if tc.Name != tool || tc.Err != nil {
			continue
		}
		responses = append(responses, tc.Response)
	}
	return responses
}

func checkProfile(p *contract.DistProfile, want map[string]float64) []string {
	if p == nil {
		return []string{"Profile 为空，无法断言"}
	}
	var fails []string
	for k, v := range want {
		got, ok := profileField(p, k)
		if !ok {
			fails = append(fails, fmt.Sprintf("profile.%s 未知字段", k))
			continue
		}
		if !floatEq(got, v) {
			fails = append(fails, fmt.Sprintf("profile.%s=%g want %g", k, got, v))
		}
	}
	return fails
}

func profileField(p *contract.DistProfile, name string) (float64, bool) {
	switch name {
	case "count":
		return float64(p.Count), true
	case "distinct":
		return float64(p.Distinct), true
	case "min":
		return p.Min, true
	case "max":
		return p.Max, true
	case "mean":
		return p.Mean, true
	case "median":
		return p.Median, true
	default:
		return 0, false
	}
}

func checkRows(data []contract.BucketRow, r dcRow) []string {
	row, ok := matchRow(data, r.Match)
	if !ok {
		return []string{fmt.Sprintf("未找到匹配行 %v", r.Match)}
	}
	var fails []string
	for k, v := range r.Expect {
		got, ok := rowField(row, k)
		if !ok {
			fails = append(fails, fmt.Sprintf("row.%s 未知字段", k))
			continue
		}
		if !floatEq(got, v) {
			fails = append(fails, fmt.Sprintf("row%v.%s=%g want %g", r.Match, k, got, v))
		}
	}
	return fails
}

func matchRow(data []contract.BucketRow, match map[string]string) (contract.BucketRow, bool) {
	for _, row := range data {
		if match["bucket"] != "" && row.Bucket == match["bucket"] {
			return row, true
		}
	}
	return contract.BucketRow{}, false
}

func rowField(row contract.BucketRow, name string) (float64, bool) {
	switch name {
	case "player_count":
		return float64(row.PlayerCount), true
	case "pct_players":
		return row.PctPlayers, true
	case "cum_pct_players":
		return row.CumPctPlayers, true
	case "total_value":
		return float64(row.TotalValue), true
	default:
		return 0, false
	}
}

func checkGroup(groups []contract.GroupProfile, g dcGroup) []string {
	var gp *contract.GroupProfile
	for i := range groups {
		if groups[i].Group == g.Match["group"] {
			gp = &groups[i]
			break
		}
	}
	if gp == nil {
		return []string{fmt.Sprintf("未找到分组 %v", g.Match)}
	}
	var fails []string
	for _, r := range g.Rows {
		fails = append(fails, checkRows(gp.Data, r)...)
	}
	if g.Profile != nil {
		fails = append(fails, checkProfile(&gp.Profile, g.Profile)...)
	}
	return fails
}

// checkTable 在 TableResult 里按 Match（列名→值字符串）找首个匹配行，再断言 Expect（列名→数值）。
func checkTable(tr *contract.TableResult, want dcTableRow) []string {
	if tr == nil {
		return []string{"Table 为空，无法断言"}
	}
	idx := make(map[string]int, len(tr.Columns))
	for i, c := range tr.Columns {
		idx[c.Name] = i
	}
	if want.SingleRow {
		if len(tr.Rows) != 1 {
			return []string{fmt.Sprintf("single_row 期望恰好 1 行，得 %d 行", len(tr.Rows))}
		}
		return checkTableExpect(tr.Rows[0], idx, map[string]string{"row": "single"}, want.Expect, want.ExpectPos, want.ExpectAny)
	}
	if len(want.Match) == 0 {
		return []string{"table 断言缺少 match（空 match 会误配首行）"}
	}
	for _, row := range tr.Rows {
		if tableRowMatches(row, idx, want.Match) {
			return checkTableExpect(row, idx, want.Match, want.Expect, want.ExpectPos, want.ExpectAny)
		}
	}
	return []string{fmt.Sprintf("未找到匹配行 %v", want.Match)}
}

func tableRowMatches(row []any, idx map[string]int, match map[string]string) bool {
	for k, v := range match {
		i, ok := idx[k]
		if !ok || i >= len(row) || cellToString(row[i]) != v {
			return false
		}
	}
	return true
}

func checkTableExpect(row []any, idx map[string]int, match map[string]string, expect map[string]float64, expectPos map[int]float64, expectAny []dcTableExpectAny) []string {
	var fails []string
	for k, v := range expect {
		i, ok := idx[k]
		if !ok || i >= len(row) {
			fails = append(fails, fmt.Sprintf("table.%s 未知列", k))
			continue
		}
		got, ok := cellToFloat(row[i])
		if !ok {
			fails = append(fails, fmt.Sprintf("table%v.%s 非数值", match, k))
			continue
		}
		if !floatEq(got, v) {
			fails = append(fails, fmt.Sprintf("table%v.%s=%g want %g", match, k, got, v))
		}
	}
	for i, v := range expectPos {
		if i < 0 || i >= len(row) {
			fails = append(fails, fmt.Sprintf("table%v.[col %d] 列越界（共 %d 列）", match, i, len(row)))
			continue
		}
		got, ok := cellToFloat(row[i])
		if !ok {
			fails = append(fails, fmt.Sprintf("table%v.[col %d] 非数值", match, i))
			continue
		}
		if !floatEq(got, v) {
			fails = append(fails, fmt.Sprintf("table%v.[col %d]=%g want %g", match, i, got, v))
		}
	}
	for _, anyExpect := range expectAny {
		fails = append(fails, checkTableExpectAny(row, idx, match, anyExpect)...)
	}
	return fails
}

func checkTableExpectAny(row []any, idx map[string]int, match map[string]string, expect dcTableExpectAny) []string {
	if len(expect.Columns) == 0 {
		return []string{fmt.Sprintf("table%v.expect_any 缺少 columns", match)}
	}
	var tried []string
	for _, col := range expect.Columns {
		i, ok := idx[col]
		if !ok || i >= len(row) {
			tried = append(tried, col+"=<missing>")
			continue
		}
		got, ok := cellToFloat(row[i])
		if !ok {
			tried = append(tried, col+"=<non-numeric>")
			continue
		}
		if floatEq(got, expect.Value) {
			return nil
		}
		tried = append(tried, fmt.Sprintf("%s=%g", col, got))
	}
	return []string{fmt.Sprintf("table%v.expect_any none of %v matched %g (tried %s)", match, expect.Columns, expect.Value, strings.Join(tried, ", "))}
}

func cellToString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", x)
	}
}

func cellToFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case int64:
		return float64(x), true
	case float64:
		return x, true
	case int:
		return float64(x), true
	}
	return 0, false
}

func floatEq(a, b float64) bool { return math.Abs(a-b) <= floatTol }

func fail(name, detail string) Score {
	return Score{Evaluator: name, Value: 0.0, Pass: false, Display: "0.00 ✗", Detail: detail}
}

var _ Evaluator = (*DataCorrectness)(nil)
