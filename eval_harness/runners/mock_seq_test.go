// framework/eval_harness/runners/mock_seq_test.go
package runners

import (
	"context"
	"strings"
	"testing"
)

func TestSequencedMockReplaysInOrder(t *testing.T) {
	m := newSequencedMock([]string{"first", "second"})
	r1, _, _, _, err := m.Call(context.Background(), "any prompt A")
	if err != nil || r1 != "first" {
		t.Fatalf("turn1 = %q err=%v, want first", r1, err)
	}
	r2, _, _, _, err := m.Call(context.Background(), "any prompt B")
	if err != nil || r2 != "second" {
		t.Fatalf("turn2 = %q err=%v, want second", r2, err)
	}
}

func TestSequencedMockExhaustedErrors(t *testing.T) {
	m := newSequencedMock([]string{"only"})
	_, _, _, _, _ = m.Call(context.Background(), "p")
	_, _, _, _, err := m.Call(context.Background(), "p")
	if err == nil || !strings.Contains(err.Error(), "exhausted") {
		t.Fatalf("expected exhausted error, got %v", err)
	}
}

func TestSequencedMockModel(t *testing.T) {
	if newSequencedMock(nil).Model() != "mock-seq" {
		t.Fatalf("unexpected model")
	}
}
