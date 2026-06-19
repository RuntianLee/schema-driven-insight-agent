package reflexion

import (
	"sort"
	"strings"

	"github.com/RuntianLee/schema-driven-insight-agent/memory"
)

// 默认权重：facet 主导（近似字典序，bm25 仅破同分），避免无关注入回流。
const (
	weightFacet = 3.0
	weightBM25  = 1.0
)

// categoryWeight 标签类别权重：shape 压过细标签。
var categoryWeight = map[string]float64{
	"shape": 3, "agg": 1, "dim": 1, "filter": 1, "cmp": 1,
}

// Reranker 对召回候选按相关度排序截断。信号可叠加（今接 facet+bm25，留 embedding/usage 位）。
type Reranker interface {
	Rerank(hits []memory.SearchResult, queryFacets []string, k int) []memory.SearchResult
}

type facetBM25Reranker struct{}

func newFacetBM25Reranker() *facetBM25Reranker { return &facetBM25Reranker{} }

// facetOverlap 按标签类别加权求和。
// 对 hitTags 中每个出现在 queryFacets 里的标签（去重），
// 取其类别（冒号前缀，如 "shape"/"agg"）对应的 categoryWeight 累加。
func facetOverlap(hitTags, queryFacets []string) float64 {
	// 构建 queryFacets 集合，便于 O(1) 查找
	qSet := make(map[string]struct{}, len(queryFacets))
	for _, f := range queryFacets {
		qSet[f] = struct{}{}
	}

	seen := make(map[string]struct{}) // 去重：同一标签只计一次
	var score float64
	for _, tag := range hitTags {
		if _, ok := seen[tag]; ok {
			continue
		}
		if _, match := qSet[tag]; !match {
			continue
		}
		seen[tag] = struct{}{}

		// 取冒号前缀作为类别
		category := tag
		if idx := strings.Index(tag, ":"); idx >= 0 {
			category = tag[:idx]
		}
		if w, ok := categoryWeight[category]; ok {
			score += w
		}
	}
	return score
}

// Rerank 对 hits 按 facetOverlap + 归一化 BM25 打分，降序稳定排序后取前 k 条返回。
//
// BM25 规则：Rank 越小越相关 → relevance = -hit.Rank；
// 在本次候选集内 min-max 归一化到 [0,1]（命名 normBM25）；
// score = weightFacet*facetOverlap + weightBM25*normBM25；
// 退化（size==1 或 max==min）时 normBM25 取 0，纯靠 facetOverlap 排序。
func (r *facetBM25Reranker) Rerank(hits []memory.SearchResult, queryFacets []string, k int) []memory.SearchResult {
	if len(hits) == 0 {
		return hits
	}

	// 计算每条候选的 relevance（翻向）并求全局 min/max
	relevances := make([]float64, len(hits))
	minR, maxR := -hits[0].Rank, -hits[0].Rank
	for i, h := range hits {
		rel := -h.Rank
		relevances[i] = rel
		if rel < minR {
			minR = rel
		}
		if rel > maxR {
			maxR = rel
		}
	}

	// 构造带原始下标的评分切片，保证稳定排序
	type scoredIdx struct {
		origIdx int
		score   float64
	}
	scored := make([]scoredIdx, len(hits))
	for i, h := range hits {
		var normBM25 float64
		if maxR > minR {
			normBM25 = (relevances[i] - minR) / (maxR - minR)
		}
		fo := facetOverlap(h.Item.Tags, queryFacets)
		scored[i] = scoredIdx{
			origIdx: i,
			score:   weightFacet*fo + weightBM25*normBM25,
		}
	}

	// 稳定降序排序（同分保持原始召回顺序）
	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	// 截断
	out := make([]memory.SearchResult, len(scored))
	for i, s := range scored {
		out[i] = hits[s.origIdx]
	}
	if k > 0 && k < len(out) {
		out = out[:k]
	}
	return out
}
