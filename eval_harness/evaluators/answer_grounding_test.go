package evaluators

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/RuntianLee/schema-driven-insight-agent/contract"
	"gopkg.in/yaml.v3"
)

func TestParseAnswerGroundingReply_FullLedger(t *testing.T) {
	raw := "```json\n" + `{"score":4,"claims":[
		{"claim":"次日流失42%","status":"grounded","evidence":"q2.churn_d1=0.42"},
		{"claim":"ARPU 12.5","status":"ungrounded","evidence":"q1..q3 无此值"}
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
	c := constJudge(`{"score":2,"claims":[{"claim":"ARPU 12.5","status":"ungrounded","evidence":"无"}],"reason":"悬空"}`)
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
