// framework/eval_harness/runners/suite_advisor_test.go
package runners

import (
	"context"
	"testing"

	"github.com/RuntianLee/schema-driven-insight-agent/contract"
	"github.com/RuntianLee/schema-driven-insight-agent/eval_harness/evaluators"
	"gopkg.in/yaml.v3"
)

// stubDispatcher 返回固定 OK Response，满足 agent.ToolDispatcher 接口。
type stubDispatcher struct{}

func (stubDispatcher) Dispatch(_ context.Context, _ string, _ map[string]any) (contract.Response, error) {
	return contract.Response{Status: contract.StatusOK}, nil
}

func TestRunSuitePopulatesAdvisory(t *testing.T) {
	var agNode yaml.Node
	_ = yaml.Unmarshal([]byte("min_items: 1"), &agNode)
	evalReg := evaluators.NewRegistry()
	evalReg.Register(evaluators.NewAdvisorGrounding())
	cfg := Config{
		Dispatcher:      stubDispatcher{},
		EvalReg:         evalReg,
		EvalOrder:       []string{"advisor_grounding"},
		AdvisorPlaybook: "高价值客户单独运营",
		Tasks: []TaskInput{{
			ID:       "t-adv",
			Question: "各国客户流失？",
			// 两轮：第一轮返回 analyze tool call JSON（驱动 Analyst），第二轮返回叙述
			LLMTurns: []string{
				`{"tool":"analyze","args":{"table":"customers"}}`,
				"德国流失高。",
			},
			// AdvisorTurn：mock Advisor 的脚本输出
			AdvisorTurn: `{"summary":"建议","items":[{"observation":"德国流失高","source_ref":"q1","action":"排查","priority":"high","caveat":"草案"}]}`,
			Evaluators:  map[string]yaml.Node{"advisor_grounding": *agNode.Content[0]},
		}},
	}
	rep, err := RunSuite(context.Background(), cfg)
	if err != nil {
		t.Fatalf("RunSuite: %v", err)
	}
	if rep.GateFailed() {
		t.Errorf("gate 应通过（草案 1 条且溯源 q1）")
	}
}
