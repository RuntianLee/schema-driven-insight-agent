package evaluators

import "testing"

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
