package evaluators

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/RuntianLee/schema-driven-insight-agent/contract"
	"github.com/RuntianLee/schema-driven-insight-agent/llm"
	"gopkg.in/yaml.v3"
)

func TestParseAnswerGroundingReply_FullLedger(t *testing.T) {
	raw := "```json\n" + `{"score":4,"claims":[
		{"claim":"次日流失42%","status":"grounded","anchor":"q2.churn_d1","kind":"cell","claimed_value":0.42},
		{"claim":"ARPU 12.5","status":"ungrounded","anchor":"","kind":"cell","claimed_value":12.5}
	],"reason":"一处悬空"}` + "\n```"
	got, err := parseAnswerGroundingReply(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if got.Score != 4 || len(got.Claims) != 2 {
		t.Fatalf("score/claims 不对: %+v", got)
	}
	if got.Claims[1].Status != "ungrounded" || got.Claims[1].Claim != "ARPU 12.5" {
		t.Fatalf("第二条主张解析错: %+v", got.Claims[1])
	}
	if got.Claims[0].Anchor != "q2.churn_d1" || got.Claims[0].ClaimedValue != 0.42 {
		t.Fatalf("结构化锚解析错: %+v", got.Claims[0])
	}
}

func TestParseAnswerGroundingReply_ScoreOutOfRange(t *testing.T) {
	if _, err := parseAnswerGroundingReply(`{"score":7,"claims":[],"reason":"x"}`); err == nil {
		t.Fatal("score=7 越界应报错")
	}
}

func TestParseAnswerGroundingReply_NotJSON(t *testing.T) {
	if _, err := parseAnswerGroundingReply("没有花括号"); err == nil {
		t.Fatal("非 JSON 应报错")
	}
}

func agSpecNode(t *testing.T, rubric string, min int) *yaml.Node {
	t.Helper()
	var n yaml.Node
	body := "rubric: " + rubric + "\nmin_score: " + strconv.Itoa(min)
	if err := yaml.Unmarshal([]byte(body), &n); err != nil {
		t.Fatal(err)
	}
	return n.Content[0]
}

func TestAnswerGrounding_NameAndKind(t *testing.T) {
	e := NewAnswerGrounding(NewMockJudge())
	if e.Name() != "answer_grounding" || e.Deterministic() {
		t.Fatalf("Name/Deterministic 不对: %s %v", e.Name(), e.Deterministic())
	}
}

func TestAnswerGrounding_BelowMin(t *testing.T) {
	c := constJudge(`{"score":2,"claims":[{"claim":"ARPU 12.5","status":"ungrounded","anchor":"","kind":"cell","claimed_value":12.5}],"reason":"悬空"}`)
	e := NewAnswerGrounding(c)
	res := TaskResult{Answer: "ARPU 12.5", ToolCalls: []contract.ToolCall{{Name: "analyze"}}}
	sc, err := e.Evaluate(context.Background(), res, agSpecNode(t, "x", 4))
	if err != nil {
		t.Fatal(err)
	}
	if sc.Value != 2.0/5.0 || !sc.BelowMin {
		t.Fatalf("Value/BelowMin 不对: %+v", sc)
	}
	if !strings.Contains(sc.Detail, "ARPU 12.5") {
		t.Fatalf("Detail 应含未接地主张: %q", sc.Detail)
	}
}

func TestAnswerGrounding_ErroredNotFolded(t *testing.T) {
	// flakyJudge{failN,calls,ok} 已存在于 judge_test.go；failN=judgeMaxAttempts → 三次全失败。
	e := NewAnswerGrounding(&flakyJudge{failN: judgeMaxAttempts})
	sc, err := e.Evaluate(context.Background(), TaskResult{}, agSpecNode(t, "x", 4))
	if err != nil {
		t.Fatal(err)
	}
	if !sc.Errored || sc.Value != 0 {
		t.Fatalf("耗尽重试应 Errored 且不向上 error: %+v", sc)
	}
}

// TestCompactResponse_ShrinksYetKeepsKeyNumbers 验证压缩器砍掉臃肿数组（TopN/嵌套 Data）
// 但保留可被答案引用的聚合统计（每组 count/mean/median/percentiles、表格行）。
func TestCompactResponse_ShrinksYetKeepsKeyNumbers(t *testing.T) {
	// 构造臃肿 Response：5 组，每组带满 TopN + 嵌套 Data（json.Marshal 会很大）。
	mkGroup := func(name string, mean, p99 float64) contract.GroupProfile {
		topN := make([]contract.TopRow, 20)
		data := make([]contract.BucketRow, 20)
		return contract.GroupProfile{
			Group:   name,
			Profile: contract.DistProfile{Count: 1000, Mean: mean, Median: mean - 5, Min: 0, Max: p99 + 100, P90: p99 - 50, P95: p99 - 10, P99: p99, TopN: topN},
			Data:    data,
		}
	}
	resp := contract.Response{
		Status: contract.StatusOK,
		Groups: []contract.GroupProfile{
			mkGroup("1", 2000, 9000), mkGroup("2", 8000, 40000),
			mkGroup("3", 3000, 12000), mkGroup("4", 5000, 22000), mkGroup("5", 1000, 4000),
		},
	}
	full, _ := json.Marshal(resp)
	compact := compactResponse(resp)
	if len(compact) >= len(full) {
		t.Fatalf("压缩应更短: compact=%d full=%d", len(compact), len(full))
	}
	// 关键数字必须保留：组 2 的 mean=8000 与 p99=40000 都该在压缩文本里。
	for _, want := range []string{"8000", "40000", "[2]", "count=1000"} {
		if !strings.Contains(compact, want) {
			t.Fatalf("压缩丢了关键信息 %q:\n%s", want, compact)
		}
	}
}

// TestAnswerGrounding_RealLLM_Discriminates 是真 LLM 判别力冒烟（阶梯①）：
// 默认 skip，仅 AG_SMOKE=1 时跑，避免日常烧 token。
// 凭证：ResolveStrict 读 AG_CONFIG 指向的配置文件；不存在则回退 MINIMAX_API_KEY 环境变量。
func TestAnswerGrounding_RealLLM_Discriminates(t *testing.T) {
	if os.Getenv("AG_SMOKE") != "1" {
		t.Skip("AG_SMOKE!=1：跳过真 LLM 冒烟")
	}
	client, err := llm.ResolveStrict(os.Getenv("AG_CONFIG")) // 同 ab.go:32 解析路径
	if err != nil {
		t.Fatalf("构造真 judge 失败（检查 AG_CONFIG 指向 minimax 配置 或 设 MINIMAX_API_KEY）: %v", err)
	}
	e := NewAnswerGrounding(client)

	// 结构化结果：server_id=1 avg_money=2000, server_id=2 avg_money=8000（无 ARPU 这列、无 12.5）
	calls := []contract.ToolCall{{Name: "analyze", Response: contract.Response{
		Status: contract.StatusOK,
		Table: &contract.TableResult{
			Columns:  []contract.ColumnMeta{{Name: "server_id"}, {Name: "avg_money"}},
			Rows:     [][]any{{1, 2000.0}, {2, 8000.0}},
			RowCount: 2,
		},
	}}}
	rubric := "逐个检查回答里的定量主张（数字/阈值/比较/比例）是否接地：逐字出现在某 qN 结果、或可由结果单元格合法派生（比例/倍数/差值/百分比）、或取整四舍五入后一致、或来自问题本身给定的阈值，均判 grounded；结果中找不到也无法派生判 ungrounded。score=5 全部接地；每出现一个未接地主张显著扣分，核心结论编造扣更重；score=1 多数定量主张悬空。"
	spec := agSpecNode(t, rubric, 4)

	// 正样本：派生量 8000/2000=4 倍合法 → 高分、无 ungrounded
	pos := TaskResult{Answer: "1 服人均 2000，2 服人均 8000，2 服人均高出 4 倍。", ToolCalls: calls}
	scPos, err := e.Evaluate(context.Background(), pos, spec)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("正样本: %s | %s", scPos.Display, scPos.Detail)
	if scPos.Value < 4.0/5.0 || strings.Contains(scPos.Detail, "未接地") {
		t.Fatalf("正样本（合法派生 4 倍）应高分无未接地: %+v", scPos)
	}

	// 负样本：ARPU 12.5 凭空 → 跌破 min 且点名 12.5
	neg := TaskResult{Answer: "1 服人均 2000，2 服人均 8000，ARPU 高达 12.5。", ToolCalls: calls}
	scNeg, err := e.Evaluate(context.Background(), neg, spec)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("负样本: %s | %s", scNeg.Display, scNeg.Detail)
	if !scNeg.BelowMin || !strings.Contains(scNeg.Detail, "12.5") {
		t.Fatalf("负样本（凭空 12.5）应 BelowMin 且 Detail 点名 12.5: %+v", scNeg)
	}
}

func TestAnswerGrounding_DeterministicLedger(t *testing.T) {
	c := constJudge(`{"score":3,"claims":[
		{"claim":"EU 人均 3000","status":"grounded","anchor":"q1.group[EU].profile.mean","kind":"cell","claimed_value":3000},
		{"claim":"ARPU 12.5","status":"ungrounded","anchor":"","kind":"cell","claimed_value":12.5}
	],"reason":"一处悬空"}`)
	e := NewAnswerGrounding(c)
	res := TaskResult{Answer: "...", ToolCalls: []contract.ToolCall{
		{Name: "analyze", Response: contract.Response{Status: contract.StatusOK,
			Groups: []contract.GroupProfile{{Group: "EU", Profile: contract.DistProfile{Mean: 3000}}}}},
	}}
	sc, err := e.Evaluate(context.Background(), res, agSpecNode(t, "x", 4))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"attribution_resolved_rate=0.50", "resolved", "unresolvable"} {
		if !strings.Contains(sc.Detail, want) {
			t.Fatalf("Detail 缺 %q:\n%s", want, sc.Detail)
		}
	}
}

func TestAnswerGrounding_CatchesJudgeOverLenientMismatch(t *testing.T) {
	c := constJudge(`{"score":5,"claims":[
		{"claim":"EU 人均 9999","status":"grounded","anchor":"q1.group[EU].profile.mean","kind":"cell","claimed_value":9999}
	],"reason":"判官误判全接地"}`)
	e := NewAnswerGrounding(c)
	res := TaskResult{Answer: "...", ToolCalls: []contract.ToolCall{
		{Name: "analyze", Response: contract.Response{Status: contract.StatusOK,
			Groups: []contract.GroupProfile{{Group: "EU", Profile: contract.DistProfile{Mean: 3000}}}}},
	}}
	sc, err := e.Evaluate(context.Background(), res, agSpecNode(t, "x", 4))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sc.Detail, "mismatch") || !strings.Contains(sc.Detail, "attribution_resolved_rate=0.00") {
		t.Fatalf("应抓出 mismatch 且 rate=0.00:\n%s", sc.Detail)
	}
}

// TestAnswerGrounding_RealLLM_Attribution 是阶梯①冒烟：默认 skip，AG_SMOKE=1 才跑。
// 验证升级后的判官在真 LLM 下：正样本派生量 resolver 判 resolved、rate 高；
// 负样本凭空数 rate 跌（mismatch 或 unresolvable）。
func TestAnswerGrounding_RealLLM_Attribution(t *testing.T) {
	if os.Getenv("AG_SMOKE") != "1" {
		t.Skip("AG_SMOKE!=1：跳过真 LLM 冒烟")
	}
	client, err := llm.ResolveStrict(os.Getenv("AG_CONFIG"))
	if err != nil {
		t.Fatalf("构造真 judge 失败（检查 AG_CONFIG / MINIMAX_API_KEY）: %v", err)
	}
	e := NewAnswerGrounding(client)
	calls := []contract.ToolCall{{Name: "analyze", Response: contract.Response{
		Status: contract.StatusOK,
		Table: &contract.TableResult{
			Columns:  []contract.ColumnMeta{{Name: "server_id"}, {Name: "avg_money"}},
			Rows:     [][]any{{1, 2000.0}, {2, 8000.0}},
			RowCount: 2,
		},
	}}}
	rubric := "逐个检查回答里的定量主张是否接地：能溯源到某 qN 单元格、或由单元格合法派生（比例/倍数/差值/百分比）即 grounded；找不到也无法派生判 ungrounded。score=5 全接地；每个未接地显著扣分。"
	spec := agSpecNode(t, rubric, 4)

	pos := TaskResult{Answer: "1 服人均 2000，2 服人均 8000，2 服高出 4 倍。", ToolCalls: calls}
	scPos, _ := e.Evaluate(context.Background(), pos, spec)
	t.Logf("正样本: %s | %s", scPos.Display, scPos.Detail)
	if !strings.Contains(scPos.Detail, "resolved") {
		t.Fatalf("正样本应至少一条 resolved:\n%s", scPos.Detail)
	}

	neg := TaskResult{Answer: "1 服人均 2000，2 服人均 8000，ARPU 高达 12.5。", ToolCalls: calls}
	scNeg, _ := e.Evaluate(context.Background(), neg, spec)
	t.Logf("负样本: %s | %s", scNeg.Display, scNeg.Detail)
	if strings.Contains(scNeg.Detail, "attribution_resolved_rate=1.00") {
		t.Fatalf("负样本（凭空 12.5）rate 不应满分:\n%s", scNeg.Detail)
	}
}

func TestAnswerGrounding_DerivedUnsupportedNotPenalizedAsHallucination(t *testing.T) {
	// 判官给一个合法但未注册的派生算子锚（harmonic_mean 未注册）→ resolver 标 derived_unsupported
	// （回退软评），不当作 mismatch 幻觉——守「不误伤合法派生量」。
	c := constJudge(`{"score":4,"claims":[
		{"claim":"EU/US 调和均值约 2000","status":"grounded","anchor":"harmonic_mean(q1.group[EU].profile.mean, q1.group[US].profile.mean)","kind":"derived","claimed_value":2000}
	],"reason":"派生量"}`)
	e := NewAnswerGrounding(c)
	res := TaskResult{Answer: "...", ToolCalls: []contract.ToolCall{
		{Name: "analyze", Response: contract.Response{Status: contract.StatusOK,
			Groups: []contract.GroupProfile{
				{Group: "EU", Profile: contract.DistProfile{Mean: 3000}},
				{Group: "US", Profile: contract.DistProfile{Mean: 1500}},
			}}},
	}}
	sc, err := e.Evaluate(context.Background(), res, agSpecNode(t, "x", 4))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sc.Detail, "derived_unsupported") {
		t.Fatalf("应标 derived_unsupported:\n%s", sc.Detail)
	}
	if strings.Contains(sc.Detail, "mismatch") {
		t.Fatalf("合法未注册派生量不应被判 mismatch:\n%s", sc.Detail)
	}
}
