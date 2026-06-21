// framework/eval_harness/evaluators/answer_grounding.go
package evaluators

import (
	"encoding/json"
	"fmt"
	"strings"
)

// claimVerdict 是判官对单个定量主张的判定 + 证据锚点。
type claimVerdict struct {
	Claim    string `json:"claim"`
	Status   string `json:"status"` // "grounded" | "ungrounded"
	Evidence string `json:"evidence"`
}

// answerGroundingReply 是判官输出的完整定量主张台账。
type answerGroundingReply struct {
	Score  int            `json:"score"`
	Claims []claimVerdict `json:"claims"`
	Reason string         `json:"reason"`
}

// parseAnswerGroundingReply 容错解析：从首个 { 起按单个 JSON 值解码（真 LLM 常包
// markdown fence / 前后缀 prose）；score 必须在 [1,5]，越界视为无效。
func parseAnswerGroundingReply(raw string) (answerGroundingReply, error) {
	start := strings.Index(raw, "{")
	if start < 0 {
		return answerGroundingReply{}, fmt.Errorf("非 JSON")
	}
	var reply answerGroundingReply
	if err := json.NewDecoder(strings.NewReader(raw[start:])).Decode(&reply); err != nil {
		return answerGroundingReply{}, fmt.Errorf("JSON 解析失败: %w", err)
	}
	if reply.Score < 1 || reply.Score > 5 {
		return answerGroundingReply{}, fmt.Errorf("score %d 越界（须 1-5）", reply.Score)
	}
	return reply, nil
}
