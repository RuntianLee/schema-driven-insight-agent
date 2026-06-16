package reflexion

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"github.com/RuntianLee/schema-driven-insight-agent/eval_harness/evaluators"
	"github.com/RuntianLee/schema-driven-insight-agent/eval_harness/runners"
	"github.com/RuntianLee/schema-driven-insight-agent/llm"
	"github.com/RuntianLee/schema-driven-insight-agent/memory"
)

const defaultPersistentMemoryMaxChars = 1200

type PersistentOptions struct {
	Adapter             string
	TaskClass           string
	ContextOptions      memory.ContextOptions
	Limit               int
	MinScore            float64
	PersistObservations bool
}

type PersistentProvider struct {
	short *Provider
	store memory.Store
	opts  PersistentOptions
}

func NewPersistent(reflectLLM llm.Client, store memory.Store, opts PersistentOptions) *PersistentProvider {
	return &PersistentProvider{
		short: New(reflectLLM),
		store: store,
		opts:  opts,
	}
}

var (
	_ runners.ReflectionProvider = (*PersistentProvider)(nil)
	_ runners.ReflectionObserver = (*PersistentProvider)(nil)
)

func (p *PersistentProvider) ContextFor(ctx context.Context, taskID, question string) (string, error) {
	shortCtx, err := p.short.ContextFor(ctx, taskID, question)
	if err != nil {
		return "", err
	}
	if p.store == nil {
		return shortCtx, nil
	}

	longCtx := p.longContextFor(ctx, taskID, question)
	switch {
	case strings.TrimSpace(shortCtx) == "":
		return longCtx, nil
	case strings.TrimSpace(longCtx) == "":
		return shortCtx, nil
	default:
		return shortCtx + "\n\n" + longCtx, nil
	}
}

func (p *PersistentProvider) Observe(ctx context.Context, res evaluators.TaskResult, scores map[string]evaluators.Score) error {
	obs, err := p.short.observeAndUpdate(ctx, res, scores)
	if err != nil {
		return err
	}
	if !p.opts.PersistObservations || p.store == nil || obs.mode == observationNone {
		return nil
	}

	item, ok := memoryItemFromObservation(p.opts, res, obs)
	if !ok {
		return nil
	}
	_, _ = p.store.Upsert(ctx, item)
	return nil
}

func (p *PersistentProvider) Reset() {
	p.short.Reset()
}

func (p *PersistentProvider) longContextFor(ctx context.Context, taskID, question string) string {
	limit := p.limit()
	seen := map[string]bool{}
	results := make([]memory.SearchResult, 0, limit)
	rounds := []struct {
		query     memory.Query
		exactTask bool
	}{
		{query: p.query(taskID, question, limit), exactTask: true},
		{query: p.query(taskID, "", limit), exactTask: true},
		{query: p.query("", question, limit), exactTask: false},
	}

	for _, round := range rounds {
		if len(results) >= limit {
			break
		}
		hits, err := p.store.Search(ctx, round.query)
		if err != nil {
			return renderReflectionMemory(results, p.opts.ContextOptions)
		}
		for _, hit := range hits {
			if round.exactTask && hit.Item.TaskID != taskID {
				continue
			}
			if hit.Item.ID == "" {
				continue
			}
			if seen[hit.Item.ID] {
				continue
			}
			seen[hit.Item.ID] = true
			results = append(results, hit)
			if len(results) >= limit {
				break
			}
		}
	}
	return renderReflectionMemory(results, p.opts.ContextOptions)
}

func (p *PersistentProvider) query(taskID, question string, limit int) memory.Query {
	return memory.Query{
		Adapter:  p.opts.Adapter,
		TaskID:   taskID,
		Question: question,
		Tags:     []string{"reflection"},
		Limit:    limit,
		MinScore: p.opts.MinScore,
	}
}

func (p *PersistentProvider) limit() int {
	if p.opts.Limit > 0 {
		return p.opts.Limit
	}
	if p.opts.ContextOptions.MaxItems > 0 {
		return p.opts.ContextOptions.MaxItems
	}
	return 5
}

func memoryItemFromObservation(opts PersistentOptions, res evaluators.TaskResult, obs observation) (memory.Item, bool) {
	if obs.mode == observationNone {
		return memory.Item{}, false
	}
	tools := toolNames(res.ToolCalls)
	summary, outline := persistentObservationText(obs, tools)
	if strings.TrimSpace(summary) == "" {
		return memory.Item{}, false
	}
	return memory.Item{
		SourceType:    "reflection",
		SourceID:      stableReflectionSourceID(opts.Adapter, res.TaskID, obs.mode, res.Question, tools, summary),
		Adapter:       opts.Adapter,
		TaskID:        res.TaskID,
		TaskClass:     opts.TaskClass,
		Question:      persistentMemoryText(res.Question),
		Summary:       summary,
		AnswerOutline: outline,
		Tools:         tools,
		Tags:          []string{"reflection", string(obs.mode)},
		Score:         0.8,
	}, true
}

func persistentObservationText(obs observation, tools []string) (summary, outline string) {
	switch obs.mode {
	case observationFixQuery:
		summary = persistentMemoryText(obs.lesson)
		outline = "下次优先校验工具形态、过滤口径、分组/聚合参数和 caveat；只把该条 memory 当作方法经验。"
	case observationRefineExplanation:
		summary = "查询口径已经通过 data_correctness；后续应复用同一查询口径，只改进解读完整性。"
		outline = "使用 " + strings.Join(tools, ", ") + " 复查同一口径，补足关键结论、量化对比、运营建议和数据局限。"
	default:
		return "", ""
	}
	return summary, persistentMemoryText(outline)
}

func toolNames(calls []evaluators.ToolCall) []string {
	seen := map[string]bool{}
	names := make([]string, 0, len(calls))
	for _, call := range calls {
		name := strings.TrimSpace(call.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names
}

func stableReflectionSourceID(adapter, taskID string, mode observationMode, question string, tools []string, body string) string {
	h := sha256.Sum256([]byte(strings.Join([]string{
		adapter,
		taskID,
		string(mode),
		strings.TrimSpace(question),
		strings.Join(tools, ","),
		strings.TrimSpace(body),
	}, "\x00")))
	return fmt.Sprintf("reflection:%s:%s:%s:%s", adapter, taskID, mode, hex.EncodeToString(h[:8]))
}

func renderReflectionMemory(results []memory.SearchResult, opts memory.ContextOptions) string {
	if len(results) == 0 {
		return ""
	}

	maxItems := opts.MaxItems
	if maxItems <= 0 || maxItems > len(results) {
		maxItems = len(results)
	}
	maxChars := opts.MaxChars
	if maxChars <= 0 {
		maxChars = defaultPersistentMemoryMaxChars
	}

	var b strings.Builder
	b.WriteString("Reflection memory（已脱敏，只作为方法经验，不作为事实或答案来源）:")
	for i := 0; i < maxItems; i++ {
		item := results[i].Item
		fmt.Fprintf(&b, "\n- [%s] Q: %s", item.ID, item.Question)
		if item.Summary != "" {
			fmt.Fprintf(&b, "\n  Summary: %s", item.Summary)
		}
		if item.AnswerOutline != "" {
			fmt.Fprintf(&b, "\n  Path: %s", item.AnswerOutline)
		}
		if len(item.Tools) > 0 {
			fmt.Fprintf(&b, "\n  Tools: %s", strings.Join(item.Tools, ", "))
		}
	}
	return truncateRunes(b.String(), maxChars)
}

func persistentMemoryText(s string) string {
	s = memory.ScrubText(strings.TrimSpace(s))
	s = redactLiteralFacts(s)
	return truncateRunes(s, 500)
}

var (
	jsonRowsPattern = regexp.MustCompile(`(?is)\{[^{}]*"rows"\s*:\s*\[.*\]\s*\}`)
	idValuePattern  = regexp.MustCompile(`(?i)\b[a-z_]*id\s*=\s*\d+\b`)
	numberPattern   = regexp.MustCompile(`\b\d+(?:\.\d+)?%?\b`)
)

func redactLiteralFacts(s string) string {
	s = jsonRowsPattern.ReplaceAllString(s, "[structured-result]")
	s = idValuePattern.ReplaceAllString(s, "[id]")
	s = numberPattern.ReplaceAllString(s, "[number]")
	return s
}

func truncateRunes(s string, max int) string {
	if max <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max])
}
