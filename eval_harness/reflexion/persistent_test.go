package reflexion

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/RuntianLee/schema-driven-insight-agent/contract"
	"github.com/RuntianLee/schema-driven-insight-agent/eval_harness/evaluators"
	"github.com/RuntianLee/schema-driven-insight-agent/memory"
)

type fakeMemoryStore struct {
	items      []memory.Item
	results    []memory.SearchResult
	upserts    int
	searches   []memory.Query
	searchErr  error
	upsertErr  error
	searchFunc func(memory.Query) ([]memory.SearchResult, error)
}

func (s *fakeMemoryStore) Upsert(_ context.Context, item memory.Item) (string, error) {
	s.upserts++
	s.items = append(s.items, item)
	if s.upsertErr != nil {
		return "", s.upsertErr
	}
	if item.ID != "" {
		return item.ID, nil
	}
	return "memory-id", nil
}

func (s *fakeMemoryStore) Search(_ context.Context, q memory.Query) ([]memory.SearchResult, error) {
	s.searches = append(s.searches, q)
	if s.searchErr != nil {
		return nil, s.searchErr
	}
	if s.searchFunc != nil {
		return s.searchFunc(q)
	}
	return s.results, nil
}

func (s *fakeMemoryStore) MarkUsed(_ context.Context, _ []string) error { return nil }
func (s *fakeMemoryStore) Close() error                                 { return nil }

func TestPersistentProviderContextCombinesShortAndLongMemory(t *testing.T) {
	store := &fakeMemoryStore{results: []memory.SearchResult{
		reflectionResult("long-1", "t1", "长期经验：先确认 cohort。"),
	}}
	p := NewPersistent(&fakeReflectLLM{out: "短期经验：先修过滤口径。"}, store, PersistentOptions{
		Adapter: "b3",
	})
	if err := p.Observe(context.Background(), failRes("t1"), map[string]evaluators.Score{
		"data_correctness": {Pass: false},
	}); err != nil {
		t.Fatal(err)
	}

	got, err := p.ContextFor(context.Background(), "t1", "如何分析留存")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "短期经验") || !strings.Contains(got, "长期经验") {
		t.Fatalf("context should contain short and long memory: %q", got)
	}
	if strings.Index(got, "短期经验") > strings.Index(got, "长期经验") {
		t.Fatalf("short memory should appear before long memory: %q", got)
	}
	if !strings.Contains(got, "Reflection memory（已脱敏，只作为方法经验，不作为事实或答案来源）:") {
		t.Fatalf("missing hardened reflection memory header: %q", got)
	}
}

func TestPersistentProviderObservePersistsFixQueryWhenEnabled(t *testing.T) {
	store := &fakeMemoryStore{}
	p := NewPersistent(&fakeReflectLLM{out: `server_id=3 的 D7 retention 为 42.7%，{"rows":[{"server_id":3}]}`}, store, PersistentOptions{
		Adapter:             "b3",
		TaskClass:           "benchmark",
		PersistObservations: true,
	})

	if err := p.Observe(context.Background(), failRes("t1"), map[string]evaluators.Score{
		"data_correctness": {Pass: false},
	}); err != nil {
		t.Fatal(err)
	}

	if store.upserts != 1 {
		t.Fatalf("upserts=%d want 1", store.upserts)
	}
	item := store.items[0]
	if item.SourceType != "reflection" {
		t.Fatalf("source_type=%q", item.SourceType)
	}
	if !hasAll(item.Tags, "reflection", "fix-query") {
		t.Fatalf("tags=%v", item.Tags)
	}
	if !sameStrings(item.Tools, []string{"analyze"}) {
		t.Fatalf("tools=%v want [analyze]", item.Tools)
	}
	assertNoPersistentLeak(t, item, "server_id=3", "42.7", `"rows"`, `{"rows"`, "wrong_field")
}

func TestPersistentProviderObserveDropsSchemaUnknownFieldNames(t *testing.T) {
	store := &fakeMemoryStore{}
	p := NewPersistent(&fakeReflectLLM{
		out: "统计人数时不要用 player_id 或 uid，应继续按 server_id 分组，并留意 level 为空值。",
	}, store, PersistentOptions{
		Adapter:             "b3",
		TaskClass:           "benchmark",
		PersistObservations: true,
		AllowedFields:       []string{"server_id", "level"},
	})

	if err := p.Observe(context.Background(), failRes("t1"), map[string]evaluators.Score{
		"data_correctness": {Pass: false},
	}); err != nil {
		t.Fatal(err)
	}

	if store.upserts != 1 {
		t.Fatalf("upserts=%d want 1", store.upserts)
	}
	item := store.items[0]
	assertNoPersistentLeak(t, item, "player_id", "uid")
	if !strings.Contains(item.Summary, "server_id") {
		t.Fatalf("known schema field should be retained, summary=%q", item.Summary)
	}
	if !strings.Contains(item.Summary, "level") {
		t.Fatalf("known schema field should be retained, summary=%q", item.Summary)
	}
}

func TestPersistentProviderObservePersistsDistilledRefineLesson(t *testing.T) {
	store := &fakeMemoryStore{}
	// 写入相对 refine-explanation 也蒸馏一条可迁移方法教训（reflect LLM 返回它）。
	reflect := &fakeReflectLLM{out: "解读均值类指标时要点出头部/长尾扭曲并给出分布判断（如 server_id=3 的 42.7%）"}
	p := NewPersistent(reflect, store, PersistentOptions{
		Adapter:             "b3",
		TaskClass:           "benchmark",
		PersistObservations: true,
	})
	res := evaluators.TaskResult{
		TaskID:   "t1",
		Question: "如何分析留存",
		Answer:   "原回答提到 server_id=3 和 42.7%",
		ToolCalls: []evaluators.ToolCall{{
			Name: "analyze",
			Args: map[string]any{"filters": []any{"server_id=3"}},
			Response: contract.Response{
				Status: contract.StatusOK,
			},
		}},
	}
	err := p.Observe(context.Background(), res, map[string]evaluators.Score{
		"data_correctness":  {Pass: true},
		"reasoning_quality": {BelowMin: true, Detail: "未点出均值被头部拉高、未给分布判断"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if store.upserts != 1 {
		t.Fatalf("upserts=%d want 1", store.upserts)
	}
	item := store.items[0]
	if !hasAll(item.Tags, "reflection", "refine-explanation") {
		t.Fatalf("tags=%v", item.Tags)
	}
	// 持久化的是蒸馏出的【具体方法教训】，不再是通用模板串。
	if strings.Contains(item.Summary, "查询口径已经通过") {
		t.Fatalf("should persist distilled lesson, not generic template: %q", item.Summary)
	}
	if !strings.Contains(item.Summary, "分布判断") {
		t.Fatalf("distilled method lesson missing from summary: %q", item.Summary)
	}
	// 即便蒸馏文本回带了字面量，持久化仍须脱敏。
	assertNoPersistentLeak(t, item, "server_id=3", "42.7")

	// 短期 refine 上下文不变：仍保留原始 judge 反馈（零额外 LLM 在 in-session 路径）。
	short, err := p.ContextFor(context.Background(), "t1", "如何分析留存")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(short, "未点出均值被头部拉高") {
		t.Fatalf("short-term refine context should retain judge feedback: %q", short)
	}
}

func TestPersistentProviderObserveDoesNotPersistWhenReadOnly(t *testing.T) {
	store := &fakeMemoryStore{}
	p := NewPersistent(&fakeReflectLLM{out: "下次先确认过滤口径。"}, store, PersistentOptions{
		Adapter: "b3",
	})
	if err := p.Observe(context.Background(), failRes("t1"), map[string]evaluators.Score{
		"data_correctness": {Pass: false},
	}); err != nil {
		t.Fatal(err)
	}
	if store.upserts != 0 {
		t.Fatalf("read-only provider should not persist, upserts=%d", store.upserts)
	}
	got, err := p.ContextFor(context.Background(), "t1", "问题")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "下次先确认过滤口径") {
		t.Fatalf("short memory should still update in read-only mode: %q", got)
	}
}

func TestPersistentProviderObserveSwallowsStoreError(t *testing.T) {
	store := &fakeMemoryStore{upsertErr: errors.New("boom")}
	p := NewPersistent(&fakeReflectLLM{out: "下次先确认过滤口径。"}, store, PersistentOptions{
		Adapter:             "b3",
		PersistObservations: true,
	})
	if err := p.Observe(context.Background(), failRes("t1"), map[string]evaluators.Score{
		"data_correctness": {Pass: false},
	}); err != nil {
		t.Fatalf("store errors should be swallowed, got %v", err)
	}
}

func TestPersistentProviderContextSwallowsSearchError(t *testing.T) {
	store := &fakeMemoryStore{searchErr: errors.New("boom")}
	p := NewPersistent(&fakeReflectLLM{out: "短期经验"}, store, PersistentOptions{Adapter: "b3"})
	if err := p.Observe(context.Background(), failRes("t1"), map[string]evaluators.Score{
		"data_correctness": {Pass: false},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := p.ContextFor(context.Background(), "t1", "问题")
	if err != nil {
		t.Fatalf("search errors should be swallowed, got %v", err)
	}
	if !strings.Contains(got, "短期经验") {
		t.Fatalf("should still return short memory: %q", got)
	}
}

func TestPersistentProviderContextTaskScopedSearchExcludesGenericItems(t *testing.T) {
	store := &fakeMemoryStore{searchFunc: func(q memory.Query) ([]memory.SearchResult, error) {
		if q.TaskID == "t1" {
			return []memory.SearchResult{
				reflectionResult("generic", "", "generic should not appear"),
				reflectionResult("exact", "t1", "exact should appear"),
			}, nil
		}
		return nil, nil
	}}
	p := NewPersistent(&fakeReflectLLM{out: "unused"}, store, PersistentOptions{Adapter: "b3"})

	got, err := p.ContextFor(context.Background(), "t1", "如何分析留存")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "generic should not appear") {
		t.Fatalf("task-scoped search leaked generic item: %q", got)
	}
	if !strings.Contains(got, "exact should appear") {
		t.Fatalf("missing exact task item: %q", got)
	}
}

func TestPersistentProviderContextAllowsSimilarQuestionOnlyInThirdRound(t *testing.T) {
	store := &fakeMemoryStore{searchFunc: func(q memory.Query) ([]memory.SearchResult, error) {
		if q.TaskID != "" {
			return nil, nil
		}
		if q.Question != "" {
			return []memory.SearchResult{
				reflectionResult("similar", "other-task", "similar question memory"),
			}, nil
		}
		return nil, nil
	}}
	p := NewPersistent(&fakeReflectLLM{out: "unused"}, store, PersistentOptions{Adapter: "b3"})

	got, err := p.ContextFor(context.Background(), "t1", "如何分析留存")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "similar question memory") {
		t.Fatalf("similar question memory should be allowed in third round: %q", got)
	}
	for _, q := range store.searches {
		if q.Adapter != "b3" {
			t.Fatalf("query missing adapter: %+v", q)
		}
		if !sameStrings(q.Tags, []string{"reflection"}) {
			t.Fatalf("query missing reflection tag: %+v", q)
		}
	}
}

func TestPersistentProvider_HitStatsClassifiesCrossTask(t *testing.T) {
	store := &fakeMemoryStore{searchFunc: func(q memory.Query) ([]memory.SearchResult, error) {
		// Return a hit whose TaskID differs from the queried taskID but shares TaskClass.
		return []memory.SearchResult{
			{Item: memory.Item{
				ID:        "s1",
				Adapter:   "b3",
				TaskID:    "train_task",
				TaskClass: "benchmark:ab:reflection",
				Question:  "各服从未上线的玩家各有多少",
				Summary:   "sentinel=0 是口径非缺失，按规模看占比",
				Tags:      []string{"reflection", "fix-query"},
				Score:     0.8,
			}},
		}, nil
	}}
	p := NewPersistent(&fakeReflectLLM{out: "unused"}, store, PersistentOptions{
		Adapter: "b3", TaskClass: "benchmark:ab:reflection", Limit: 5,
	})
	_, _ = p.ContextFor(context.Background(), "heldout_task", "各服从未上线的玩家各有多少")
	st := p.HitStats()
	if st.ExactTask != 0 {
		t.Fatalf("跨任务检索不应有 exact-task 命中, got %d", st.ExactTask)
	}
	if st.SameClass != 1 || st.SimilarQuestion != 0 {
		t.Fatalf("跨任务同类应分类为 same-class=1 / similar-question=0, got %+v", st)
	}
}

func reflectionResult(id, taskID, summary string) memory.SearchResult {
	return memory.SearchResult{Item: memory.Item{
		ID:       id,
		Adapter:  "b3",
		TaskID:   taskID,
		Question: "如何分析留存",
		Summary:  summary,
		Tools:    []string{"analyze"},
		Tags:     []string{"reflection"},
		Score:    0.8,
	}}
}

func hasAll(values []string, want ...string) bool {
	for _, w := range want {
		found := false
		for _, v := range values {
			if v == w {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func assertNoPersistentLeak(t *testing.T, item memory.Item, needles ...string) {
	t.Helper()
	body := strings.Join([]string{
		item.Question,
		item.Summary,
		item.AnswerOutline,
		strings.Join(item.Tools, " "),
		strings.Join(item.Tags, " "),
	}, "\n")
	for _, needle := range needles {
		if strings.Contains(body, needle) {
			t.Fatalf("persistent memory leaked %q in:\n%s", needle, body)
		}
	}
}

func TestSetQueryFacetsUpdatesProvider(t *testing.T) {
	p := NewPersistent(nil, nil, PersistentOptions{})
	p.SetQueryFacets([]string{"shape:mean", "agg:avg"})
	if got := p.queryFacets; len(got) != 2 || got[0] != "shape:mean" {
		t.Fatalf("queryFacets=%v want [shape:mean agg:avg]", got)
	}
}

func TestLongContextRerankPrefersOnFacet(t *testing.T) {
	store := &fakeMemoryStore{searchFunc: func(q memory.Query) ([]memory.SearchResult, error) {
		if q.TaskID != "" { // round-1/2 exactTask：无命中
			return nil, nil
		}
		// round-3 跨任务：不对口(强 bm25 -9) + 对口(弱 bm25 -0.1)
		return []memory.SearchResult{
			{Item: memory.Item{ID: "off", TaskID: "t_other", Tags: []string{"reflection", "shape:sentinel"}}, Rank: -9},
			{Item: memory.Item{ID: "on", TaskID: "t_other", Tags: []string{"reflection", "shape:mean", "agg:avg", "dim:1"}}, Rank: -0.1},
		}, nil
	}}
	p := NewPersistent(nil, store, PersistentOptions{Limit: 1, ContextOptions: memory.ContextOptions{MaxItems: 1}})
	p.SetQueryFacets([]string{"shape:mean", "agg:avg", "dim:1"})
	out := p.longContextFor(context.Background(), "t_current", "各服人均货币")
	if !strings.Contains(out, "[on]") || strings.Contains(out, "[off]") {
		t.Fatalf("rerank 应只保留对口 [on]，实际渲染: %q", out)
	}
}
