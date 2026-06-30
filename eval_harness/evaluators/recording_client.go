// framework/eval_harness/evaluators/recording_client.go
package evaluators

import (
	"context"
	"time"

	"github.com/RuntianLee/schema-driven-insight-agent/llm"
)

// LLMTokenRecorder 是 recordingClient 落 token 所需的最小接口；
// trajectory.Recorder / agent.TrajectoryStore 结构化满足之。
type LLMTokenRecorder interface {
	RecordLLMCallRole(role, prompt, response, model string, tokensIn, tokensOut int,
		costUSD float64, started, ended time.Time, err error)
}

type recorderKeyType struct{}

var recorderKey recorderKeyType

// ContextWithRecorder 由 suite runner 每任务调用，把该任务 recorder 注入 ctx。
func ContextWithRecorder(ctx context.Context, rec LLMTokenRecorder) context.Context {
	return context.WithValue(ctx, recorderKey, rec)
}

func recorderFromCtx(ctx context.Context) LLMTokenRecorder {
	rec, _ := ctx.Value(recorderKey).(LLMTokenRecorder)
	return rec
}

// recordingClient 包 judge client：Call 透传 + 把 token 以 role 标签落 ctx recorder。
// 实现 llm.JudgeProfiler 以与构造器内 tuneJudge 正交组合（记录壳留最外、profile 套内层）。
type recordingClient struct {
	inner llm.Client
	role  string
}

// NewRecordingClient 包一个 client，给其 Call 贴角色标签并落 ctx recorder。
func NewRecordingClient(inner llm.Client, role string) llm.Client {
	return recordingClient{inner: inner, role: role}
}

func (c recordingClient) Call(ctx context.Context, prompt string) (string, int, int, float64, error) {
	t0 := time.Now()
	resp, tin, tout, cost, err := c.inner.Call(ctx, prompt)
	if rec := recorderFromCtx(ctx); rec != nil {
		rec.RecordLLMCallRole(c.role, prompt, resp, c.inner.Model(), tin, tout, cost, t0, time.Now(), err)
	}
	return resp, tin, tout, cost, err
}

func (c recordingClient) Model() string { return c.inner.Model() }

func (c recordingClient) WithJudgeProfile(maxTokens int, disableThinking bool) llm.Client {
	if pr, ok := c.inner.(llm.JudgeProfiler); ok {
		return recordingClient{inner: pr.WithJudgeProfile(maxTokens, disableThinking), role: c.role}
	}
	return c
}

var (
	_ llm.Client        = recordingClient{}
	_ llm.JudgeProfiler = recordingClient{}
)
