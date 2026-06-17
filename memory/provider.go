package memory

import "context"

// Provider retrieves memory-backed context for reflection prompts.
type Provider struct {
	Store    Store
	Adapter  string
	Options  ContextOptions
	MinScore float64
	Limit    int
}

// ContextFor returns rendered memory context for a task question.
func (p Provider) ContextFor(ctx context.Context, taskID, question string) (string, error) {
	if p.Store == nil {
		return "", nil
	}

	limit := p.Limit
	if limit <= 0 {
		limit = p.Options.MaxItems
	}
	if limit <= 0 {
		limit = 5
	}

	results, err := p.Store.Search(ctx, Query{
		Adapter:  p.Adapter,
		TaskID:   taskID,
		Question: question,
		Limit:    limit,
		MinScore: p.MinScore,
	})
	if err != nil || len(results) == 0 {
		return "", nil
	}

	return RenderContext(results, p.Options), nil
}
