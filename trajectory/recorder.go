package trajectory

import (
	"context"
	"database/sql"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

const channelBuffer = 256 // trajectory-spec-v2 §6：单任务 10-50 step × 5 倍余量

// Recorder = agent.TrajectoryStore 的实现（结构化类型桥接）。
type Recorder interface {
	TrajectoryID() string
	RecordLLMCall(prompt, response, model string, tokensIn, tokensOut int,
		costUSD float64, started, ended time.Time, err error)
	RecordToolCall(toolName string, input, output any, started, ended time.Time, err error)
	RecordReasoning(thought string, started, ended time.Time)
	Finalize(ctx context.Context, outcome, finalOutput, errSummary string) error
}

type stepRecord struct {
	stepIndex int
	stepType  string
	input     any
	output    any
	started   time.Time
	ended     time.Time
	err       error
	tokensIn  int
	tokensOut int
	costUSD   float64
	model     string
	toolName  string
}

type recorder struct {
	db        *sql.DB
	trajID    string
	startTime time.Time

	mu        sync.Mutex
	stepIndex int

	ch        chan stepRecord
	done      chan struct{}
	closeOnce sync.Once
	dropped   int64

	// 仅 writer goroutine 写，done 关闭后才读（无竞争）
	totalTokens int
	totalCost   float64
	stepCount   int
}

// New 开启新 trajectory：同步插入占位行 + 启动异步 step writer（trajectory-spec-v2 §6）。
// taskClass：production（真实使用）/ benchmark（eval 基准跑）；空串落 NULL。
func New(ctx context.Context, db *sql.DB, agentVersion, question, taskClass string) (Recorder, error) {
	id := uuid.NewString()
	_, err := db.ExecContext(ctx,
		`INSERT INTO trajectories (trajectory_id, created_at, agent_version, input_question, outcome, task_class)
		 VALUES (?, ?, ?, ?, 'in_progress', ?)`,
		id, time.Now().Unix(), agentVersion, question, nullable(taskClass))
	if err != nil {
		return nil, err
	}
	r := &recorder{
		db:        db,
		trajID:    id,
		startTime: time.Now(),
		ch:        make(chan stepRecord, channelBuffer),
		done:      make(chan struct{}),
	}
	go r.writeLoop()
	return r, nil
}

func (r *recorder) TrajectoryID() string { return r.trajID }

func (r *recorder) writeLoop() {
	defer close(r.done)
	for s := range r.ch {
		r.persistStep(s)
		r.totalTokens += s.tokensIn + s.tokensOut
		r.totalCost += s.costUSD
		r.stepCount++
	}
}

func (r *recorder) persistStep(s stepRecord) {
	inJSON, _ := json.Marshal(s.input)
	outJSON, _ := json.Marshal(s.output)
	var errStr any
	if s.err != nil {
		errStr = s.err.Error()
	}
	_, err := r.db.Exec(
		`INSERT INTO trajectory_steps
		 (step_id, trajectory_id, step_index, step_type, started_at, ended_at, latency_ms,
		  input, output, tokens_input, tokens_output, cost_usd, model_name, tool_name, error)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		uuid.NewString(), r.trajID, s.stepIndex, s.stepType,
		s.started.UnixMilli(), s.ended.UnixMilli(), s.ended.Sub(s.started).Milliseconds(),
		string(inJSON), string(outJSON), s.tokensIn, s.tokensOut, s.costUSD,
		nullable(s.model), nullable(s.toolName), errStr)
	if err != nil {
		// 永不干扰主流程（trajectory-spec-v2 §2 #4）：仅吞掉。
		return
	}
}

func (r *recorder) enqueue(s stepRecord) {
	r.mu.Lock()
	s.stepIndex = r.stepIndex
	r.stepIndex++
	r.mu.Unlock()
	select {
	case r.ch <- s:
	default:
		atomic.AddInt64(&r.dropped, 1) // channel 满则丢弃，不阻塞主流程
	}
}

func (r *recorder) RecordLLMCall(prompt, response, model string, tokensIn, tokensOut int,
	costUSD float64, started, ended time.Time, err error) {
	ev := Redact(Event{Kind: "llm_call", Input: prompt, Output: response})
	r.enqueue(stepRecord{
		stepType: "llm_call", input: ev.Input, output: ev.Output,
		started: started, ended: ended, err: err,
		tokensIn: tokensIn, tokensOut: tokensOut, costUSD: costUSD, model: model,
	})
}

func (r *recorder) RecordToolCall(toolName string, input, output any, started, ended time.Time, err error) {
	ev := Redact(Event{Kind: "tool_call", Input: input, Output: output})
	r.enqueue(stepRecord{
		stepType: "tool_call", input: ev.Input, output: ev.Output,
		started: started, ended: ended, err: err, toolName: toolName,
	})
}

func (r *recorder) RecordReasoning(thought string, started, ended time.Time) {
	ev := Redact(Event{Kind: "reasoning", Input: thought})
	r.enqueue(stepRecord{stepType: "reasoning", input: ev.Input, started: started, ended: ended})
}

// Finalize 关闭 channel、等 writer 排空、回填汇总（唯一允许的 UPDATE 路径）。可幂等调用。
func (r *recorder) Finalize(ctx context.Context, outcome, finalOutput, errSummary string) error {
	r.closeOnce.Do(func() {
		close(r.ch)
		<-r.done
	})
	_, err := r.db.ExecContext(ctx,
		`UPDATE trajectories
		 SET final_output=?, outcome=?, total_tokens=?, total_cost_usd=?,
		     total_latency_ms=?, step_count=?, error_summary=?
		 WHERE trajectory_id=?`,
		finalOutput, outcome, r.totalTokens, r.totalCost,
		time.Since(r.startTime).Milliseconds(), r.stepCount, nullable(errSummary), r.trajID)
	return err
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
