// Package seedgen 按声明式 seed.yaml 生成确定性合成 Layer2 快照（cmd/seed 的核心）。
// 取代每个 adapter 手写的 seed-fixture：无真库即可体验 agent 全流程。
// 仅支持整型列生成器（const / enum 加权 / buckets 加权区间）；列间相互独立。
package seedgen

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

// DefaultSeed：缺省 RNG 种子（固定 → 同 spec 输出字节级可复现）。
const DefaultSeed int64 = 2026611

type Spec struct {
	Seed   int64                `yaml:"seed"`
	AsOf   int64                `yaml:"as_of"`
	Tables map[string]TableSpec `yaml:"tables"`
}

type TableSpec struct {
	Rows    int                  `yaml:"rows"`
	Columns map[string]Generator `yaml:"columns"`
}

// Generator 三选一：const / enum / buckets。
type Generator struct {
	Const   *int64        `yaml:"const"`
	Enum    []WeightedVal `yaml:"enum"`
	Buckets []BucketGen   `yaml:"buckets"`
}

type WeightedVal struct {
	Value  int64 `yaml:"value"`
	Weight int64 `yaml:"weight"` // 缺省 1
}

type BucketGen struct {
	Min    int64  `yaml:"min"`
	Max    int64  `yaml:"max"`
	Weight int64  `yaml:"weight"` // 缺省 1
	Skew   string `yaml:"skew"`   // "" uniform | cube 偏 min | recent 偏 max
}

func ParseSpec(b []byte) (*Spec, error) {
	sp := &Spec{Seed: DefaultSeed}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(sp); err != nil {
		return nil, fmt.Errorf("seed spec parse: %w", err)
	}
	if len(sp.Tables) == 0 {
		return nil, fmt.Errorf("seed spec: tables 为空")
	}
	for tname, tb := range sp.Tables {
		if tb.Rows <= 0 {
			return nil, fmt.Errorf("seed spec %s: rows 必须 > 0", tname)
		}
		for col, g := range tb.Columns {
			if err := validateGenerator(g); err != nil {
				return nil, fmt.Errorf("seed spec %s.%s: %w", tname, col, err)
			}
		}
	}
	return sp, nil
}

func validateGenerator(g Generator) error {
	n := 0
	if g.Const != nil {
		n++
	}
	if len(g.Enum) > 0 {
		n++
	}
	if len(g.Buckets) > 0 {
		n++
	}
	if n != 1 {
		return fmt.Errorf("生成器须恰好声明 const/enum/buckets 之一，发现 %d 个", n)
	}
	for _, b := range g.Buckets {
		if b.Min > b.Max {
			return fmt.Errorf("bucket min %d > max %d", b.Min, b.Max)
		}
		if b.Skew != "" && b.Skew != "cube" && b.Skew != "recent" {
			return fmt.Errorf("skew %q 不在白名单（cube|recent）", b.Skew)
		}
		if b.Weight < 0 {
			return fmt.Errorf("weight 不可为负")
		}
	}
	for _, v := range g.Enum {
		if v.Weight < 0 {
			return fmt.Errorf("weight 不可为负")
		}
	}
	return nil
}
