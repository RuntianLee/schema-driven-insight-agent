package eino_agent

import (
	"bufio"
	"context"
	"fmt"
	"io"
)

// degradeMessage 是非交互道（无真人可答）返回给模型的降级指令：推模型走「假设兜底」。
const degradeMessage = "非交互模式：无法反问。请基于最合理假设直接作答，并在报告顶部显式标注本次假设。"

// Clarifier 把模型发起的澄清问题投递给「答复源」并取回答复。
// 它不是 agent、不含 LLM——只是连到真人 stdin 或降级串的哑管道。
type Clarifier interface {
	// Ask 投递 question，返回答复文本（真人答复或降级串）。
	Ask(ctx context.Context, question string) string
}

// StdinClarifier 把 question 打印到 out，从 scanner（真人 stdin）读一行作答复。
// EOF/非 TTY（scanner.Scan() 返回 false）→ 返回 degradeMessage，使 -q 自动化优雅降级。
type StdinClarifier struct {
	scanner *bufio.Scanner
	out     io.Writer
}

// NewStdinClarifier 用共享的 stdin scanner 与输出流构造。REPL 与 StdinClarifier
// 必须共享同一个 scanner，避免双缓冲抢读 stdin。
func NewStdinClarifier(scanner *bufio.Scanner, out io.Writer) *StdinClarifier {
	return &StdinClarifier{scanner: scanner, out: out}
}

// Ask 把 Scan 放进 goroutine 以能响应 ctx 取消：select 同时等结果与 ctx.Done()。
// ctx 取消时返回 degradeMessage（与 EOF/非 TTY 同一降级口径）+ 不阻塞——澄清失败不应崩 Run。
// 已知权衡：ctx 取消后 goroutine 里残留的 Scan 仍会继续阻塞等 stdin（无法中途打断系统调用），
// 直到真有一行输入或 stdin 关闭才退出；不影响本次 Ask 已返回的调用方。
func (c *StdinClarifier) Ask(ctx context.Context, question string) string {
	fmt.Fprintf(c.out, "\n[需要澄清] %s\n> ", question)
	result := make(chan string, 1)
	go func() {
		if !c.scanner.Scan() {
			result <- degradeMessage
			return
		}
		result <- c.scanner.Text()
	}()
	select {
	case ans := <-result:
		return ans
	case <-ctx.Done():
		return degradeMessage
	}
}

// NonInteractiveClarifier 恒返回 degradeMessage：eval harness 等无真人道注入，
// 保证澄清门不阻塞确定性 gate。
type NonInteractiveClarifier struct{}

func (NonInteractiveClarifier) Ask(_ context.Context, _ string) string { return degradeMessage }

var (
	_ Clarifier = (*StdinClarifier)(nil)
	_ Clarifier = NonInteractiveClarifier{}
)
