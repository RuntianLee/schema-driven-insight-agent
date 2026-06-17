package memory

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestStoreUpsertSearchAndMarkUsed(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	id, err := store.Upsert(ctx, Item{
		SourceType:    "manual",
		Adapter:       "b3",
		TaskID:        "retention",
		TaskClass:     "benchmark",
		Question:      "How should whale retention analyze cohorts?",
		Summary:       "Use whale retention analyze for payer cohorts.",
		AnswerOutline: "Call analyze and compare whale retention over time.",
		Tools:         []string{"analyze"},
		Tags:          []string{"retention"},
		Score:         0.9,
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if id == "" {
		t.Fatal("upsert returned empty id")
	}

	results, err := store.Search(ctx, Query{
		Adapter:  "b3",
		TaskID:   "retention",
		Question: "whale retention analyze",
		Limit:    3,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results len=%d want 1: %#v", len(results), results)
	}
	if results[0].Item.ID != id {
		t.Fatalf("result id=%q want %q", results[0].Item.ID, id)
	}
	if results[0].Snippet == "" {
		t.Fatal("snippet is empty")
	}

	if err := store.MarkUsed(ctx, []string{id}); err != nil {
		t.Fatalf("mark used: %v", err)
	}
	results, err = store.Search(ctx, Query{
		Adapter:  "b3",
		TaskID:   "retention",
		Question: "whale retention analyze",
		Limit:    3,
	})
	if err != nil {
		t.Fatalf("search after mark used: %v", err)
	}
	if results[0].Item.UsedCount != 1 {
		t.Fatalf("used count=%d want 1", results[0].Item.UsedCount)
	}
	if results[0].Item.LastUsedAt.IsZero() {
		t.Fatal("last used at is zero")
	}
}

func TestStoreUpsertBySourceIsIdempotent(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	firstID, err := store.Upsert(ctx, Item{
		SourceType: "trajectory",
		SourceID:   "traj-1",
		Adapter:    "b3",
		TaskID:     "retention",
		Question:   "old retention query",
		Summary:    "old retention summary",
		Tools:      []string{"analyze"},
		Tags:       []string{"retention"},
		Score:      0.2,
	})
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	secondID, err := store.Upsert(ctx, Item{
		SourceType: "trajectory",
		SourceID:   "traj-1",
		Adapter:    "b3",
		TaskID:     "retention",
		Question:   "updated activation query",
		Summary:    "updated activation summary",
		Tools:      []string{"analyze"},
		Tags:       []string{"activation"},
		Score:      0.8,
	})
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if secondID != firstID {
		t.Fatalf("second id=%q want %q", secondID, firstID)
	}

	results, err := store.Search(ctx, Query{
		Adapter:  "b3",
		Question: "activation",
		Limit:    5,
	})
	if err != nil {
		t.Fatalf("search updated term: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results len=%d want 1: %#v", len(results), results)
	}
	if results[0].Item.ID != firstID {
		t.Fatalf("result id=%q want %q", results[0].Item.ID, firstID)
	}
	if results[0].Item.Question != "updated activation query" {
		t.Fatalf("question=%q want updated activation query", results[0].Item.Question)
	}
	if results[0].Item.Score != 0.8 {
		t.Fatalf("score=%v want 0.8", results[0].Item.Score)
	}
}

func TestStoreUpsertBySourceIsAdapterScoped(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	b3ID, err := store.Upsert(ctx, Item{
		SourceType: "manual",
		SourceID:   "shared-note",
		Adapter:    "b3",
		Question:   "b3 retention note",
		Summary:    "b3 retention memory",
		Tools:      []string{"analyze"},
		Tags:       []string{"retention"},
	})
	if err != nil {
		t.Fatalf("upsert b3: %v", err)
	}
	tdID, err := store.Upsert(ctx, Item{
		SourceType: "manual",
		SourceID:   "shared-note",
		Adapter:    "td",
		Question:   "td retention note",
		Summary:    "td retention memory",
		Tools:      []string{"analyze"},
		Tags:       []string{"retention"},
	})
	if err != nil {
		t.Fatalf("upsert td: %v", err)
	}
	if b3ID == tdID {
		t.Fatalf("adapter-scoped source ids should create distinct memory rows, got %q", b3ID)
	}

	b3Results, err := store.Search(ctx, Query{Adapter: "b3", Question: "retention", Limit: 5})
	if err != nil {
		t.Fatalf("search b3: %v", err)
	}
	if len(b3Results) != 1 || b3Results[0].Item.ID != b3ID {
		t.Fatalf("b3 results=%#v want id %q", b3Results, b3ID)
	}
	tdResults, err := store.Search(ctx, Query{Adapter: "td", Question: "retention", Limit: 5})
	if err != nil {
		t.Fatalf("search td: %v", err)
	}
	if len(tdResults) != 1 || tdResults[0].Item.ID != tdID {
		t.Fatalf("td results=%#v want id %q", tdResults, tdID)
	}
}

func TestStoreUpsertBySourceConcurrent(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	const workers = 16
	start := make(chan struct{})
	ids := make(chan string, workers)
	errs := make(chan error, workers)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			id, err := store.Upsert(ctx, Item{
				SourceType: "trajectory",
				SourceID:   "traj-concurrent",
				Adapter:    "b3",
				TaskID:     "retention",
				Question:   "concurrent whale retention query",
				Summary:    "concurrent whale retention summary",
				Tools:      []string{"analyze"},
				Tags:       []string{"retention"},
				Score:      0.8,
			})
			if err != nil {
				errs <- err
				return
			}
			ids <- id
		}()
	}
	close(start)
	wg.Wait()
	close(ids)
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent upsert failed: %v", err)
		}
	}
	var first string
	seen := 0
	for id := range ids {
		seen++
		if id == "" {
			t.Fatal("concurrent upsert returned empty id")
		}
		if first == "" {
			first = id
			continue
		}
		if id != first {
			t.Fatalf("id=%q want %q", id, first)
		}
	}
	if seen != workers {
		t.Fatalf("saw %d ids want %d", seen, workers)
	}

	var count int
	err := store.db.QueryRowContext(ctx,
		`SELECT count(*) FROM memory_items WHERE source_type = 'trajectory' AND source_id = 'traj-concurrent'`,
	).Scan(&count)
	if err != nil {
		t.Fatalf("count source rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("source row count=%d want 1", count)
	}

	results, err := store.Search(ctx, Query{Adapter: "b3", Question: "concurrent retention", Limit: 3})
	if err != nil {
		t.Fatalf("search concurrent item: %v", err)
	}
	if len(results) != 1 || results[0].Item.ID != first {
		t.Fatalf("unexpected concurrent result: %#v want id %q", results, first)
	}
}

func TestStoreSearchFiltersAdapterAndTool(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	items := []Item{
		{
			SourceType: "manual",
			Adapter:    "b3",
			TaskID:     "retention",
			Question:   "retention with analyze",
			Summary:    "b3 analyze retention summary",
			Tools:      []string{"analyze"},
			Tags:       []string{"retention"},
			Score:      0.7,
		},
		{
			SourceType: "manual",
			Adapter:    "td",
			TaskID:     "retention",
			Question:   "retention with analyze",
			Summary:    "td analyze retention summary",
			Tools:      []string{"analyze"},
			Tags:       []string{"retention"},
			Score:      0.7,
		},
		{
			SourceType: "manual",
			Adapter:    "b3",
			TaskID:     "retention",
			Question:   "retention with query distribution",
			Summary:    "b3 query distribution retention summary",
			Tools:      []string{"query_distribution"},
			Tags:       []string{"retention"},
			Score:      0.7,
		},
	}
	wantID, err := store.Upsert(ctx, items[0])
	if err != nil {
		t.Fatalf("upsert b3 analyze: %v", err)
	}
	for i := 1; i < len(items); i++ {
		if _, err := store.Upsert(ctx, items[i]); err != nil {
			t.Fatalf("upsert item %d: %v", i, err)
		}
	}

	results, err := store.Search(ctx, Query{
		Adapter:  "b3",
		Question: "retention",
		Tools:    []string{"analyze"},
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("search filtered: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results len=%d want 1: %#v", len(results), results)
	}
	if results[0].Item.ID != wantID {
		t.Fatalf("result id=%q want %q", results[0].Item.ID, wantID)
	}
}

func TestStoreUpsertScrubsPrivateText(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	leaks := []string{
		"123456789",
		"abcdef1234567890",
		"this_is_a_very_long_secret_literal_value",
	}

	_, err := store.Upsert(ctx, Item{
		SourceType:    "manual",
		Adapter:       "b3",
		TaskID:        "privacy",
		Question:      "玩家 123456789 asked about private retention",
		Summary:       "hash abcdef1234567890 showed private retention behavior",
		AnswerOutline: "SELECT * FROM t WHERE note = 'this_is_a_very_long_secret_literal_value'",
		Tools:         []string{"analyze"},
		Tags:          []string{"privacy"},
	})
	if err != nil {
		t.Fatalf("upsert private item: %v", err)
	}

	results, err := store.Search(ctx, Query{
		Adapter:  "b3",
		TaskID:   "privacy",
		Question: "private retention",
		Limit:    3,
	})
	if err != nil {
		t.Fatalf("search private item: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results len=%d want 1", len(results))
	}

	for name, value := range map[string]string{
		"Question":      results[0].Item.Question,
		"Summary":       results[0].Item.Summary,
		"AnswerOutline": results[0].Item.AnswerOutline,
	} {
		for _, leak := range leaks {
			if strings.Contains(value, leak) {
				t.Fatalf("%s leaked %q in %q", name, leak, value)
			}
		}
	}
}

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	if err := Migrate(db); err != nil {
		db.Close()
		t.Fatalf("migrate memory db: %v", err)
	}
	store := NewSQLiteStore(db)
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
	})
	return store
}
