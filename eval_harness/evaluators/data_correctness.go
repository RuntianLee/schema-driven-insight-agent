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
	Match        map[string]string  `yaml:"match"`
	Expect       map[string]float64 `yaml:"expect"`        // 按列名（别名）断言：确定性 mock 道用
	ExpectPos    map[int]float64    `yaml:"expect_pos"`    // 按列绝对位置断言：真 LLM 道别名鲁棒（agent 自选 as 别名时仍可比对）
	ExpectAny    []dcTableExpectAny `yaml:"expect_any"`    // 候选列名任一命中即可，避免 count 等前置列造成列序误判
	ExpectValues []dcValueBind      `yaml:"expect_values"` // 名字优先、列名全不存在则按值兜底（(d'') 值存在性原语）
	SingleRow    bool               `yaml:"single_row"`    // 断言唯一行：聚合 shape 无区分列可 match 时用
}

type dcTableExpectAny struct {
	Columns []string `yaml:"columns"`
	Value   float64  `yaml:"value"`
}

// dcValueBind 是 (d'') 值存在性原语的一个期望量：先按 Candidates 列名强绑定，
// 候选列名一个都不存在于本行时退化为「Value 出现在本行某个未占用 cell」。
type dcValueBind struct {
	Candidates []string `yaml:"candidates"`
	Value      float64  `yaml:"value"`
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
	if err := validateSpec(sp); err != nil {
		return Score{}, err
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

// validateSpec 对互斥/退化配置 fail-fast：any_of 与顶层 table/rows/groups 互斥；
// 同一 table 行内 single_row 与 match 互斥（single_row 已断言唯一行，match 多余且冲突）。
// 注意：顶层 expect_status / profile 是「强制前置条件」，不进入 any_of 的 OR 逻辑——
// 即便某分支通过，前置条件失败整体仍 FAIL（见 checkResponse）；dcAltBlock 内的 profile 才参与 OR。
func validateSpec(sp dcSpec) error {
	if len(sp.AnyOf) > 0 && (len(sp.Table) > 0 || len(sp.Rows) > 0 || len(sp.Groups) > 0) {
		return fmt.Errorf("data_correctness: any_of 与顶层 table/rows/groups 互斥")
	}
	for i, b := range sp.AnyOf {
		if len(b.Table) == 0 && len(b.Rows) == 0 && len(b.Groups) == 0 && b.Profile == nil {
			return fmt.Errorf("data_correctness: any_of[%d] 为空分支（无任何断言）", i)
		}
	}
	checkTableRows := func(rows []dcTableRow) error {
		for _, r := range rows {
			if r.SingleRow && len(r.Match) > 0 {
				return fmt.Errorf("data_correctness: single_row 与 match 互斥")
			}
			for _, b := range r.ExpectValues {
				if len(b.Candidates) == 0 {
					return fmt.Errorf("data_correctness: expect_values 的 bind 缺少 candidates")
				}
			}
		}
		return nil
	}
	if err := checkTableRows(sp.Table); err != nil {
		return err
	}
	for _, b := range sp.AnyOf {
		if err := checkTableRows(b.Table); err != nil {
			return err
		}
	}
	return nil
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

// matchRow 遍历 match 的全部 key（对齐 tableRowMatches 的通用语义）：每个 key 须在
// BucketRow 有对应字段且值相等，否则该行判不匹配——避免拼错/多余 key 被静默忽略成
// "只按 bucket 比对就通过"的假阳性。空 match 视为不匹配任何行（沿用既有行为边界）。
func matchRow(data []contract.BucketRow, match map[string]string) (contract.BucketRow, bool) {
	if len(match) == 0 {
		return contract.BucketRow{}, false
	}
	for _, row := range data {
		if bucketRowMatches(row, match) {
			return row, true
		}
	}
	return contract.BucketRow{}, false
}

// bucketRowMatches 检查 match 的全部 key 是否都命中 row 的对应字段。
func bucketRowMatches(row contract.BucketRow, match map[string]string) bool {
	for k, v := range match {
		got, ok := bucketRowStringField(row, k)
		if !ok || got != v {
			return false
		}
	}
	return true
}

// bucketRowStringField 取 BucketRow 的字符串键字段（match 语法当前只支持 bucket/group）。
func bucketRowStringField(row contract.BucketRow, name string) (string, bool) {
	switch name {
	case "bucket":
		return row.Bucket, true
	case "group":
		return row.Group, true
	default:
		return "", false
	}
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
		return checkTableExpect(tr.Rows[0], idx, map[string]string{"row": "single"}, want.Expect, want.ExpectPos, want.ExpectAny, want.ExpectValues)
	}
	if len(want.Match) == 0 {
		return []string{"table 断言缺少 match（空 match 会误配首行）"}
	}
	for _, row := range tr.Rows {
		if tableRowMatches(row, idx, want.Match) {
			return checkTableExpect(row, idx, want.Match, want.Expect, want.ExpectPos, want.ExpectAny, want.ExpectValues)
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

func checkTableExpect(row []any, idx map[string]int, match map[string]string, expect map[string]float64, expectPos map[int]float64, expectAny []dcTableExpectAny, expectValues []dcValueBind) []string {
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
	fails = append(fails, checkExpectValues(row, idx, match, expectValues)...)
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

// checkExpectValues 两相断言：① 名字绑定相——候选列名存在则值必须匹配，否则 FAIL（不兜底）；
// ② 值存在性兜底相——仅对候选列名全不存在的量，在本行某个「未被占用」的数值 cell 找等于 Value 的格子。
// 名字命中和兜底命中共享 claimed 集，兜底量不得复用已占用 cell（distinct-cell）。
// 使用既有 floatEq 做值比对、不修改容差逻辑（守恒：只扩寻址、不改判定）。
func checkExpectValues(row []any, idx map[string]int, match map[string]string, binds []dcValueBind) []string {
	var fails []string
	claimed := make(map[int]bool)
	var fallback []dcValueBind
	for _, b := range binds {
		col, i, present := firstPresentCandidate(row, idx, b.Candidates)
		if !present {
			fallback = append(fallback, b)
			continue
		}
		got, ok := cellToFloat(row[i])
		if !ok {
			fails = append(fails, fmt.Sprintf("table%v.%s 非数值", match, col))
			continue
		}
		if !floatEq(got, b.Value) {
			fails = append(fails, fmt.Sprintf("table%v.%s=%g want %g", match, col, got, b.Value))
			continue
		}
		// 仅名字绑定成功（值正确）才占用 cell；名字相 FAIL 时该 cell 不占用，但整体已有 fail、兜底相无法翻案，故不影响判定。
		claimed[i] = true
	}
	for _, b := range fallback {
		i, found := firstUnclaimedValueCell(row, claimed, b.Value)
		if !found {
			fails = append(fails, fmt.Sprintf("table%v.expect_values 值 %g 未在未占用单元格出现（候选列名 %v 均不存在；行内未占用数值: %v）", match, b.Value, b.Candidates, unclaimedValues(row, claimed)))
			continue
		}
		claimed[i] = true
	}
	return fails
}

// firstPresentCandidate 返回首个存在于本行的候选列名及其 cell 下标。
func firstPresentCandidate(row []any, idx map[string]int, candidates []string) (string, int, bool) {
	for _, c := range candidates {
		if i, ok := idx[c]; ok && i < len(row) {
			return c, i, true
		}
	}
	return "", 0, false
}

// firstUnclaimedValueCell 在未被 claimed 的 cell 中找首个等于 v 的数值格子。
func firstUnclaimedValueCell(row []any, claimed map[int]bool, v float64) (int, bool) {
	for i := range row {
		if claimed[i] {
			continue
		}
		if got, ok := cellToFloat(row[i]); ok && floatEq(got, v) {
			return i, true
		}
	}
	return 0, false
}

// unclaimedValues 收集本行未被 claimed 且可转数值的 cell 值，用于兜底失败时的排障信息。
func unclaimedValues(row []any, claimed map[int]bool) []float64 {
	var vals []float64
	for i := range row {
		if claimed[i] {
			continue
		}
		if got, ok := cellToFloat(row[i]); ok {
			vals = append(vals, got)
		}
	}
	return vals
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
