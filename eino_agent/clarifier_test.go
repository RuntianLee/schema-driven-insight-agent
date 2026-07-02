package eino_agent

import (
	"bufio"
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

func TestStdinClarifier_ReadsAnswer(t *testing.T) {
	sc := bufio.NewScanner(strings.NewReader("虚拟货币\n"))
	c := NewStdinClarifier(sc, io.Discard)
	got := c.Ask(context.Background(), "你指虚拟货币还是金币？")
	if got != "虚拟货币" {
		t.Fatalf("Ask=%q want 虚拟货币", got)
	}
}

func TestStdinClarifier_EOFDegrades(t *testing.T) {
	sc := bufio.NewScanner(strings.NewReader("")) // 立即 EOF
	c := NewStdinClarifier(sc, io.Discard)
	got := c.Ask(context.Background(), "问？")
	if got != degradeMessage {
		t.Fatalf("EOF 应降级为 degradeMessage，got=%q", got)
	}
}

func TestNonInteractiveClarifier_AlwaysDegrades(t *testing.T) {
	c := NonInteractiveClarifier{}
	got := c.Ask(context.Background(), "任意问题")
	if got != degradeMessage {
		t.Fatalf("Ask=%q want degradeMessage", got)
	}
}

// TestStdinClarifier_CtxCancelDoesNotHang 锁 M-2：ctx 取消时 Ask 须立即返回降级串，
// 不得阻塞等 stdin（scanner.Scan() 永不返回时也不能挂住调用方）。
func TestStdinClarifier_CtxCancelDoesNotHang(t *testing.T) {
	sc := bufio.NewScanner(newBlockingReader()) // 永不产出数据、也不 EOF：模拟真实阻塞 stdin
	c := NewStdinClarifier(sc, io.Discard)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 取消先于 Ask 调用，验证立即返回而非等 Scan

	done := make(chan string, 1)
	go func() { done <- c.Ask(ctx, "问？") }()

	select {
	case got := <-done:
		if got != degradeMessage {
			t.Fatalf("ctx 取消应返回降级串，got=%q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Ask 未在 ctx 取消后及时返回（疑似仍阻塞等 stdin）")
	}
}

// blockingReader 永不写入数据也不返回 EOF，模拟真实交互式 stdin 的阻塞行为。
type blockingReader struct{ block chan struct{} }

func newBlockingReader() *blockingReader { return &blockingReader{block: make(chan struct{})} }

func (r *blockingReader) Read(p []byte) (int, error) {
	<-r.block // 永久阻塞（测试进程退出时随 goroutine 一起回收，符合 clarifier.go 里已知权衡的注释）
	return 0, io.EOF
}
