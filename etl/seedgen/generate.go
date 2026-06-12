package seedgen

import (
	"math/rand/v2"
	"sort"
)

// apportion 把 total 行按权重分配到各档（最大余数法，确定性、和恒等于 total）。
func apportion(total int, weights []int64) []int {
	var wsum int64
	for _, w := range weights {
		if w == 0 {
			w = 1 // 缺省权重 1
		}
		wsum += w
	}
	counts := make([]int, len(weights))
	type rem struct {
		idx  int
		frac float64
	}
	rems := make([]rem, 0, len(weights))
	assigned := 0
	for i, w := range weights {
		if w == 0 {
			w = 1
		}
		exact := float64(total) * float64(w) / float64(wsum)
		c := int(exact)
		counts[i] = c
		assigned += c
		rems = append(rems, rem{i, exact - float64(c)})
	}
	sort.SliceStable(rems, func(a, b int) bool { return rems[a].frac > rems[b].frac })
	for k := 0; k < total-assigned; k++ {
		counts[rems[k%len(rems)].idx]++
	}
	return counts
}

// genColumn 为一列生成 rows 个值（档内生成 → 全列洗牌，列间独立）。
func genColumn(rng *rand.Rand, g Generator, rows int) []int64 {
	out := make([]int64, 0, rows)
	switch {
	case g.Const != nil:
		for i := 0; i < rows; i++ {
			out = append(out, *g.Const)
		}
	case len(g.Enum) > 0:
		weights := make([]int64, len(g.Enum))
		for i, v := range g.Enum {
			weights[i] = v.Weight
		}
		for i, c := range apportion(rows, weights) {
			for k := 0; k < c; k++ {
				out = append(out, g.Enum[i].Value)
			}
		}
	default: // buckets（ParseSpec 已保证三选一）
		weights := make([]int64, len(g.Buckets))
		for i, b := range g.Buckets {
			weights[i] = b.Weight
		}
		for i, c := range apportion(rows, weights) {
			b := g.Buckets[i]
			span := b.Max - b.Min + 1
			for k := 0; k < c; k++ {
				var off int64
				switch b.Skew {
				case "cube": // r³ 偏向 min（长尾分布近似）
					r := rng.Float64()
					off = int64(r * r * r * float64(span-1))
				case "recent": // 1-(1-r)² 偏向 max（留存/近期在线形态）
					r := rng.Float64()
					off = int64((1 - (1-r)*(1-r)) * float64(span-1))
				default:
					off = rng.Int64N(span)
				}
				out = append(out, b.Min+off)
			}
		}
	}
	rng.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}
