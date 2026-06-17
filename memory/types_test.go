package memory

import (
	"context"
	"testing"
)

type testStore struct{}

func (testStore) Upsert(context.Context, Item) (string, error) {
	return "id", nil
}

func (testStore) Search(context.Context, Query) ([]SearchResult, error) {
	return nil, nil
}

func (testStore) MarkUsed(context.Context, []string) error {
	return nil
}

func (testStore) Close() error {
	return nil
}

func TestStoreInterfaceUpsertReturnsID(t *testing.T) {
	var s Store = testStore{}
	id, err := s.Upsert(context.Background(), Item{})
	if err != nil || id != "id" {
		t.Fatalf("id=%q err=%v", id, err)
	}
}
