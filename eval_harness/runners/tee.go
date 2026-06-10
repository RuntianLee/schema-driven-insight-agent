// framework/eval_harness/runners/tee.go
package runners

import (
	"context"
	"time"

	"github.com/RuntianLee/schema-driven-insight-agent/agent"
)

// teeStore 把 trajectory 事件同时扇给内存 captureStore（喂 evaluator，不变）与真
// trajectory.Recorder（落 SQLite）。eino_agent 0 改动：靠注入此 store 的 opener。
type teeStore struct {
	cap *captureStore
	rec agent.TrajectoryStore // 真 trajectory.Recorder（结构化桥接）
}

func newTeeStore(cap *captureStore, rec agent.TrajectoryStore) *teeStore {
	return &teeStore{cap: cap, rec: rec}
}

// TrajectoryID 返回持久化侧 id（供 eval_results 关联）。
func (t *teeStore) TrajectoryID() string { return t.rec.TrajectoryID() }

func (t *teeStore) RecordLLMCall(prompt, response, model string, tokensIn, tokensOut int,
	costUSD float64, started, ended time.Time, err error) {
	t.cap.RecordLLMCall(prompt, response, model, tokensIn, tokensOut, costUSD, started, ended, err)
	t.rec.RecordLLMCall(prompt, response, model, tokensIn, tokensOut, costUSD, started, ended, err)
}

func (t *teeStore) RecordToolCall(name string, input, output any, started, ended time.Time, err error) {
	t.cap.RecordToolCall(name, input, output, started, ended, err)
	t.rec.RecordToolCall(name, input, output, started, ended, err)
}

func (t *teeStore) RecordReasoning(thought string, started, ended time.Time) {
	t.cap.RecordReasoning(thought, started, ended)
	t.rec.RecordReasoning(thought, started, ended)
}

func (t *teeStore) Finalize(ctx context.Context, outcome, finalOutput, errSummary string) error {
	// captureStore.Finalize 恒返回 nil（仅内存赋值，无错可报），故忽略其返回值；
	// 以持久化侧 recorder 的 Finalize 错误为准。
	_ = t.cap.Finalize(ctx, outcome, finalOutput, errSummary)
	return t.rec.Finalize(ctx, outcome, finalOutput, errSummary)
}

var _ agent.TrajectoryStore = (*teeStore)(nil)
