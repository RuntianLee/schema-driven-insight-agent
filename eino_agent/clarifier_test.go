package eino_agent

import (
	"bufio"
	"context"
	"io"
	"strings"
	"testing"
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
