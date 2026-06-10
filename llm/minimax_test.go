package llm

import "testing"

func TestNewMiniMaxModelDefault(t *testing.T) {
	c := NewMiniMax("fake-key", "")
	if c.Model() != "MiniMax-M2.7" {
		t.Fatalf("default model = %q, want \"MiniMax-M2.7\"", c.Model())
	}
	if NewMiniMax("k", "custom").Model() != "custom" {
		t.Fatal("explicit model override failed")
	}
}
