package reflexion

import (
	"testing"

	"github.com/RuntianLee/schema-driven-insight-agent/memory"
)

// sr 辅助构造 SearchResult，简化测试用例书写。
func sr(id string, tags []string, rank float64) memory.SearchResult {
	return memory.SearchResult{
		Item: memory.Item{
			ID:   id,
			Tags: tags,
		},
		Rank: rank,
	}
}

// TestFacetOverlapWeighted 验证按类别加权求和的语义。
// query = [shape:sentinel, agg:count, filter:null]
//
//	hit=[shape:sentinel,agg:count,filter:null] → 3+1+1 = 5
//	hit=[shape:mean,agg:avg]                  → 0（均不在 query 中）
//	hit=[shape:sentinel,agg:avg]              → 3（仅 shape:sentinel 命中）
func TestFacetOverlapWeighted(t *testing.T) {
	query := []string{"shape:sentinel", "agg:count", "filter:null"}

	cases := []struct {
		name     string
		hitTags  []string
		expected float64
	}{
		{
			name:     "全命中：shape+agg+filter",
			hitTags:  []string{"shape:sentinel", "agg:count", "filter:null"},
			expected: 5,
		},
		{
			name:     "零命中：shape 和 agg 均不在 query",
			hitTags:  []string{"shape:mean", "agg:avg"},
			expected: 0,
		},
		{
			name:     "部分命中：仅 shape:sentinel",
			hitTags:  []string{"shape:sentinel", "agg:avg"},
			expected: 3,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := facetOverlap(c.hitTags, query)
			if got != c.expected {
				t.Errorf("facetOverlap(%v, %v) = %v, want %v", c.hitTags, query, got, c.expected)
			}
		})
	}
}

// TestRerankFacetDominantOrderAndTruncate 验证 facet 主导压过 bm25，以及 k 截断。
//
//	query = [shape:mean, agg:avg, dim:1]
//	A: Tags=[shape:mean,agg:avg,dim:1], Rank=-0.1（对口但 bm25 弱）
//	B: Tags=[shape:sentinel],           Rank=-9.0（不对口但 bm25 强）
//
// Rerank(in, q, 1) top-1 应为 A-onshape，证明 facet 主导。
func TestRerankFacetDominantOrderAndTruncate(t *testing.T) {
	query := []string{"shape:mean", "agg:avg", "dim:1"}

	A := sr("A-onshape", []string{"shape:mean", "agg:avg", "dim:1"}, -0.1)
	B := sr("B-offshape", []string{"shape:sentinel"}, -9.0)

	reranker := newFacetBM25Reranker()
	result := reranker.Rerank([]memory.SearchResult{A, B}, query, 1)

	if len(result) != 1 {
		t.Fatalf("期望截断为 1 条，实际得到 %d 条", len(result))
	}
	if result[0].Item.ID != "A-onshape" {
		t.Errorf("top-1 期望 A-onshape，实际得到 %s（facet 未主导 bm25）", result[0].Item.ID)
	}
}
