// framework/eval_harness/evaluators/data_correctness.go
package evaluators

import (
	"context"
	"fmt"
	"math"
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

type dcSpec struct {
	Tool         string             `yaml:"tool"`
	ExpectStatus string             `yaml:"expect_status"`
	Profile      map[string]float64 `yaml:"profile"`
	Rows         []dcRow            `yaml:"rows"`
	Groups       []dcGroup          `yaml:"groups"`
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
	resp, ok := findToolResponse(res, sp.Tool)
	if !ok {
		return fail(d.Name(), fmt.Sprintf("未找到 tool %q 的调用", sp.Tool)), nil
	}

	var fails []string
	if sp.ExpectStatus != "" && string(resp.Status) != sp.ExpectStatus {
		fails = append(fails, fmt.Sprintf("status=%s want %s", resp.Status, sp.ExpectStatus))
	}
	if sp.Profile != nil {
		fails = append(fails, checkProfile(resp.Profile, sp.Profile)...)
	}
	for _, r := range sp.Rows {
		fails = append(fails, checkRows(resp.Data, r)...)
	}
	for _, g := range sp.Groups {
		fails = append(fails, checkGroup(resp.Groups, g)...)
	}

	if len(fails) > 0 {
		return fail(d.Name(), strings.Join(fails, "; ")), nil
	}
	return Score{Evaluator: d.Name(), Value: 1.0, Pass: true, Display: "1.00 ✓"}, nil
}

// findToolResponse 选取 tool 的最终有效调用：agent 允许 SCHEMA_ERROR 后自修正重试
// （prompt 约定），故优先取最后一次成功（Err==nil 且 Status==OK）的同名调用；
// 无成功调用则回退最后一次 Err==nil 的调用（保留失败 Response 供 expect_status 断言）。
func findToolResponse(res TaskResult, tool string) (contract.Response, bool) {
	var lastOK, lastNoErr *contract.Response
	for i := range res.ToolCalls {
		tc := res.ToolCalls[i]
		if tc.Name != tool || tc.Err != nil {
			continue
		}
		resp := tc.Response
		lastNoErr = &resp
		if resp.Status == contract.StatusOK {
			lastOK = &resp
		}
	}
	if lastOK != nil {
		return *lastOK, true
	}
	if lastNoErr != nil {
		return *lastNoErr, true
	}
	return contract.Response{}, false
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

func floatEq(a, b float64) bool { return math.Abs(a-b) <= floatTol }

func fail(name, detail string) Score {
	return Score{Evaluator: name, Value: 0.0, Pass: false, Display: "0.00 ✗", Detail: detail}
}

var _ Evaluator = (*DataCorrectness)(nil)
