package memory

import (
	"context"
	"time"
)

// Item is one scrubbed memory case stored for future retrieval.
type Item struct {
	ID            string
	SourceType    string
	SourceID      string
	Adapter       string
	TaskID        string
	TaskClass     string
	Question      string
	Summary       string
	AnswerOutline string
	Tools         []string
	Tags          []string
	Score         float64
	UsedCount     int
	LastUsedAt    time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Query describes filters and ranking hints for memory search.
type Query struct {
	Adapter  string
	TaskID   string
	Question string
	Tools    []string
	Tags     []string
	Limit    int
	MinScore float64
}

// SearchResult is one ranked memory hit.
type SearchResult struct {
	Item    Item
	Rank    float64
	Snippet string
}

// ContextOptions controls how retrieved memories are rendered into prompts.
type ContextOptions struct {
	MaxItems int
	MaxChars int
}

// Store is the long-term memory persistence boundary.
type Store interface {
	Upsert(ctx context.Context, item Item) (string, error)
	Search(ctx context.Context, q Query) ([]SearchResult, error)
	MarkUsed(ctx context.Context, ids []string) error
	Close() error
}
