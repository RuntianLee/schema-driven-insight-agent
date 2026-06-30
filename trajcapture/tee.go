// trajcapture/tee.go
package trajcapture

import (
	"context"
	"time"

	"github.com/RuntianLee/schema-driven-insight-agent/agent"
)

// Tee 把 trajectory 事件同时扇给内存 Capture 与真 trajectory.Recorder（落 SQLite）。
type Tee struct {
	cap *Capture
	rec agent.TrajectoryStore
}

func NewTee(cap *Capture, rec agent.TrajectoryStore) *Tee { return &Tee{cap: cap, rec: rec} }

func (t *Tee) TrajectoryID() string { return t.rec.TrajectoryID() }

func (t *Tee) RecordLLMCall(prompt, response, model string, tokensIn, tokensOut int,
	costUSD float64, started, ended time.Time, err error) {
	t.cap.RecordLLMCall(prompt, response, model, tokensIn, tokensOut, costUSD, started, ended, err)
	t.rec.RecordLLMCall(prompt, response, model, tokensIn, tokensOut, costUSD, started, ended, err)
}

func (t *Tee) RecordLLMCallRole(role, prompt, response, model string, tokensIn, tokensOut int,
	costUSD float64, started, ended time.Time, err error) {
	t.cap.RecordLLMCallRole(role, prompt, response, model, tokensIn, tokensOut, costUSD, started, ended, err)
	t.rec.RecordLLMCallRole(role, prompt, response, model, tokensIn, tokensOut, costUSD, started, ended, err)
}

func (t *Tee) RecordToolCall(name string, input, output any, started, ended time.Time, err error) {
	t.cap.RecordToolCall(name, input, output, started, ended, err)
	t.rec.RecordToolCall(name, input, output, started, ended, err)
}

func (t *Tee) RecordReasoning(thought string, started, ended time.Time) {
	t.cap.RecordReasoning(thought, started, ended)
	t.rec.RecordReasoning(thought, started, ended)
}

func (t *Tee) Finalize(ctx context.Context, outcome, finalOutput, errSummary string) error {
	_ = t.cap.Finalize(ctx, outcome, finalOutput, errSummary) // 恒 nil（仅内存赋值）
	return t.rec.Finalize(ctx, outcome, finalOutput, errSummary)
}

var _ agent.TrajectoryStore = (*Tee)(nil)
