package memory

import (
	"context"
	"testing"
)

func TestCJKSegment(t *testing.T) {
	cases := []struct{ in, want string }{
		{"各服从未", "各服 服从 从未"},
		{"各", "各"},
		{"", ""},
		{"各服abc", "各服 abc"},
		{"last_online_time=0", "last_online_time 0"},
		{"玩家 retention", "玩家 retention"},
		{"ABC", "abc"}, // ASCII lowercased
	}
	for _, c := range cases {
		if got := cjkSegment(c.in); got != c.want {
			t.Errorf("cjkSegment(%q)=%q want %q", c.in, got, c.want)
		}
	}
}

// TestSearchChineseCrossQuestionRecall is the core fix: a held-out Chinese
// question (different surface text) must retrieve a stored training lesson via
// shared CJK bigrams, with bm25 ranking the most-overlapping lesson first.
func TestSearchChineseCrossQuestionRecall(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	noID, err := store.Upsert(ctx, Item{
		SourceType: "reflection", SourceID: "no1", Adapter: "b3",
		TaskID: "never_online_quality_insight", TaskClass: "benchmark:ab",
		Question: "各服从未上线的玩家各有多少",
		Summary:  "哨兵口径：0 表示从未发生而非缺失，按规模看占比",
		Tags:     []string{"reflection"}, Score: 0.8,
	})
	if err != nil {
		t.Fatalf("upsert never_online: %v", err)
	}
	if _, err := store.Upsert(ctx, Item{
		SourceType: "reflection", SourceID: "ar1", Adapter: "b3",
		TaskID: "arpu_gap_insight", TaskClass: "benchmark:ab",
		Question: "各服玩家的人均虚拟货币是多少",
		Summary:  "均值口径：被头部拉高，要看分布",
		Tags:     []string{"reflection"}, Score: 0.8,
	}); err != nil {
		t.Fatalf("upsert arpu: %v", err)
	}

	// Held-out HO4 question (different surface) shares 各服/服从/从未/的玩/玩家/各有/有多/多少 bigrams.
	results, err := store.Search(ctx, Query{
		Adapter:  "b3",
		Question: "各服从未通关无尽试炼的玩家各有多少",
		Limit:    5,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("held-out Chinese question retrieved nothing (Chinese FTS recall broken)")
	}
	if results[0].Item.ID != noID {
		t.Fatalf("top result=%q want never_online %q (bm25 should rank highest bigram overlap first)",
			results[0].Item.ID, noID)
	}
}
