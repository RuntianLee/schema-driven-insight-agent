package seedgen

import (
	"math/rand/v2"
	"reflect"
	"testing"
)

func TestApportion_ExactCounts(t *testing.T) {
	got := apportion(1000, []int64{600, 300, 80, 20})
	if !reflect.DeepEqual(got, []int{600, 300, 80, 20}) {
		t.Errorf("权重即计数时应精确: %v", got)
	}
	got = apportion(10, []int64{1, 1, 1})
	sum := 0
	for _, c := range got {
		sum += c
	}
	if sum != 10 {
		t.Errorf("配额和必须等于 rows: %v", got)
	}
}

func TestGenColumn_Deterministic(t *testing.T) {
	g := Generator{Enum: []WeightedVal{{Value: 50, Weight: 600}, {Value: 500, Weight: 400}}}
	a := genColumn(rand.New(rand.NewPCG(1, 2)), g, 1000)
	b := genColumn(rand.New(rand.NewPCG(1, 2)), g, 1000)
	if !reflect.DeepEqual(a, b) {
		t.Error("同种子必须逐值一致")
	}
	count50 := 0
	for _, v := range a {
		if v == 50 {
			count50++
		}
	}
	if count50 != 600 {
		t.Errorf("enum 权重精确配额: 50 出现 %d 次, want 600", count50)
	}
}

func TestGenColumn_BucketsRange(t *testing.T) {
	g := Generator{Buckets: []BucketGen{{Min: 0, Max: 100, Weight: 7}, {Min: 1000, Max: 2000, Weight: 3, Skew: "cube"}}}
	vals := genColumn(rand.New(rand.NewPCG(3, 4)), g, 100)
	inLow, inHigh := 0, 0
	for _, v := range vals {
		switch {
		case v >= 0 && v <= 100:
			inLow++
		case v >= 1000 && v <= 2000:
			inHigh++
		default:
			t.Fatalf("值越界: %d", v)
		}
	}
	if inLow != 70 || inHigh != 30 {
		t.Errorf("bucket 配额 70/30, got %d/%d", inLow, inHigh)
	}
}
