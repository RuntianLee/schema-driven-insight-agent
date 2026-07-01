package llm

import (
	"math"
	"testing"
)

func TestCostUSD(t *testing.T) {
	got := CostUSD(1000, 1000)
	want := costPerKTokenIn + costPerKTokenOut // 1k in + 1k out
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("CostUSD(1000,1000)=%v want %v", got, want)
	}
	if CostUSD(0, 0) != 0 {
		t.Fatalf("CostUSD(0,0) should be 0")
	}
}
