// framework/eval_harness/evalcli/ab_test.go
package evalcli

import (
	"context"
	"strings"
	"testing"

	"github.com/RuntianLee/schema-driven-insight-agent/eval_harness/evaluators"
)

// fakeLLM 无状态、由 prompt 驱动：
//   - prompt 已含工具返回（"row_count" / "\"status\""）→ 返回最终答案，结束本轮 agent 循环；
//   - 否则发一次 analyze tool-call：prompt 含 "REFLECT"（reflection 开）→ group_by server_id（命中 golden）；
//     无 reflection → group_by wrong_field（analyze 返回 SCHEMA_ERROR → data_correctness fail）。
type fakeLLM struct{}

func (fakeLLM) Model() string { return "fake" }
func (fakeLLM) Call(_ context.Context, prompt string) (string, int, int, float64, error) {
	if strings.Contains(prompt, "row_count") || strings.Contains(prompt, "\"status\"") {
		return "最终洞察：各服玩家数已给出。", 10, 10, 0, nil
	}
	field := "wrong_field"
	if strings.Contains(prompt, "REFLECT") {
		field = "server_id"
	}
	return `{"tool":"analyze","args":{"table":"player_basics","group_by":["` + field + `"],"aggregates":[{"fn":"count","as":"n"}]}}`, 10, 10, 0, nil
}

// fakeProvider 注入含 "REFLECT" 标记的上下文，使 config B 的 agent 选对分组字段。
type fakeProvider struct{}

func (fakeProvider) ContextFor(_ context.Context, _, _ string) (string, error) {
	return "REFLECT: 过往经验提示用 server_id 分组", nil
}

func TestRunAB_ProviderRaisesPassRate(t *testing.T) {
	opts := Options{
		Adapter:    "test",
		SchemaPath: "testdata/ab/schema.yaml",
		TasksDir:   "testdata/ab/tasks",
	}
	ab, err := runABWithClients(opts, fakeLLM{}, evaluators.NewMockJudge(), fakeProvider{}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if ab.MeanPassRateA != 0 {
		t.Fatalf("config A（无 reflection，查 wrong_field）应通过率 0，得 %g", ab.MeanPassRateA)
	}
	if ab.MeanPassRateB != 1 {
		t.Fatalf("config B（reflection 查 server_id）应通过率 1，得 %g", ab.MeanPassRateB)
	}
	if ab.MeanPassRateB <= ab.MeanPassRateA {
		t.Fatalf("reflection 应抬高通过率：A=%g B=%g", ab.MeanPassRateA, ab.MeanPassRateB)
	}
}
