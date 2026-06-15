package memory

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeStore struct {
	query   Query
	results []SearchResult
	err     error
}

func (s *fakeStore) Upsert(ctx context.Context, item Item) (string, error) {
	return item.ID, nil
}

func (s *fakeStore) Search(ctx context.Context, q Query) ([]SearchResult, error) {
	s.query = q
	return s.results, s.err
}

func (s *fakeStore) MarkUsed(ctx context.Context, ids []string) error {
	return nil
}

func (s *fakeStore) Close() error {
	return nil
}

func TestProviderContextForSearchesAndRenders(t *testing.T) {
	store := &fakeStore{
		results: []SearchResult{
			{
				Item: Item{
					ID:       "mem-1",
					Question: "How did whale retention get inspected before?",
					Summary:  "Use cohort retention and inspect rescue-event annotations.",
				},
			},
		},
	}
	provider := Provider{
		Store:   store,
		Adapter: "b3",
		Options: ContextOptions{MaxItems: 2, MaxChars: 300},
	}

	got, err := provider.ContextFor(context.Background(), "big_r_retention", "How to inspect whale retention?")
	if err != nil {
		t.Fatalf("ContextFor returned error: %v", err)
	}
	if !strings.Contains(got, "Memory context") {
		t.Fatalf("ContextFor should render memory header, got %q", got)
	}
	if !strings.Contains(got, "Use cohort retention") {
		t.Fatalf("ContextFor should render summary, got %q", got)
	}
	if store.query.Adapter != "b3" {
		t.Fatalf("Search Adapter = %q, want b3", store.query.Adapter)
	}
	if store.query.TaskID != "big_r_retention" {
		t.Fatalf("Search TaskID = %q, want big_r_retention", store.query.TaskID)
	}
	if store.query.Question != "How to inspect whale retention?" {
		t.Fatalf("Search Question = %q, want requested question", store.query.Question)
	}
	if store.query.Limit != 2 {
		t.Fatalf("Search Limit = %d, want 2", store.query.Limit)
	}
}

func TestProviderContextForEmptyAndErrorReturnsEmpty(t *testing.T) {
	tests := []struct {
		name  string
		store *fakeStore
	}{
		{
			name:  "empty results",
			store: &fakeStore{},
		},
		{
			name:  "search error",
			store: &fakeStore{err: errors.New("search failed")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := Provider{Store: tt.store}

			got, err := provider.ContextFor(context.Background(), "task", "question")
			if err != nil {
				t.Fatalf("ContextFor returned error: %v", err)
			}
			if got != "" {
				t.Fatalf("ContextFor = %q, want empty string", got)
			}
		})
	}
}

func TestProviderImplementsReflectionProviderShape(t *testing.T) {
	type reflectionProviderShape interface {
		ContextFor(ctx context.Context, taskID, question string) (string, error)
	}

	var _ reflectionProviderShape = Provider{}
}
