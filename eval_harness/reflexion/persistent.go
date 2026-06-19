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
	AllowedFields       []string
}

// HitStats records how many memory hits were classified during retrieval.
type HitStats struct {
	ExactTask       int // hit.Item.TaskID == queried taskID
	SameClass       int // different task but same TaskClass as provider opts
	SimilarQuestion int // cross-task, different class (everything else)
}

type PersistentProvider struct {
	short       *Provider
	store       memory.Store
	opts        PersistentOptions
	hits        HitStats
	reranker    Reranker
	queryFacets []string
}

func NewPersistent(reflectLLM llm.Client, store memory.Store, opts PersistentOptions) *PersistentProvider {
	return &PersistentProvider{
		short:    New(reflectLLM),
		store:    store,
		opts:     opts,
		reranker: newFacetBM25Reranker(),
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

	// refine-explanation：写入相额外蒸馏一条【可迁移的解读方法教训】（而非固定模板串），
	// 让长期记忆携带具体口径/分布/结构经验。仅在写入路径付这次 LLM 调用，in-session refine 仍零额外调用。
	if obs.mode == observationRefineExplanation {
		if lesson := p.distillRefineLesson(ctx, res, obs.refine.feedback); lesson != "" {
			obs.lesson = lesson
		}
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

// HitStats returns the cumulative memory hit classification counts for this provider's lifetime.
func (p *PersistentProvider) HitStats() HitStats { return p.hits }

// SetQueryFacets 设置当前任务的口径标签（每任务切换时由 runner 调用，驱动跨任务召回的软重排）。
func (p *PersistentProvider) SetQueryFacets(facets []string) { p.queryFacets = facets }

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
			switch {
			case hit.Item.TaskID == taskID:
				p.hits.ExactTask++
			case p.opts.TaskClass != "" && hit.Item.TaskClass == p.opts.TaskClass:
				p.hits.SameClass++
			default:
				p.hits.SimilarQuestion++
			}
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
	summary, outline := persistentObservationText(obs, tools, opts.AllowedFields)
	if strings.TrimSpace(summary) == "" {
		return memory.Item{}, false
	}
	return memory.Item{
		SourceType:    "reflection",
		SourceID:      stableReflectionSourceID(opts.Adapter, res.TaskID, obs.mode, res.Question, tools, summary),
		Adapter:       opts.Adapter,
		TaskID:        res.TaskID,
		TaskClass:     opts.TaskClass,
		Question:      persistentMemoryText(res.Question, opts.AllowedFields),
		Summary:       summary,
		AnswerOutline: outline,
		Tools:         tools,
		Tags:          []string{"reflection", string(obs.mode)},
		Score:         0.8,
	}, true
}

func persistentObservationText(obs observation, tools []string, allowedFields []string) (summary, outline string) {
	switch obs.mode {
	case observationFixQuery:
		summary = persistentMemoryText(obs.lesson, allowedFields)
		outline = "下次优先校验工具形态、过滤口径、分组/聚合参数和 caveat；只把该条 memory 当作方法经验。"
	case observationRefineExplanation:
		if strings.TrimSpace(obs.lesson) != "" {
			summary = persistentMemoryText(obs.lesson, allowedFields)
		} else {
			summary = "查询口径已经通过 data_correctness；后续应复用同一查询口径，只改进解读完整性。"
		}
		outline = "使用 " + strings.Join(tools, ", ") + " 复查同一口径，补足关键结论、量化对比、运营建议和数据局限。"
	default:
		return "", ""
	}
	return summary, persistentMemoryText(outline, allowedFields)
}

// distillRefineLesson 调 reflect LLM 把"查询已对、解读偏弱"的 judge 反馈蒸成一条可迁移的
// 解读方法教训。调用失败或空则返回 ""（调用方回退到通用模板）。复用 short.reflectLLM（同包）。
func (p *PersistentProvider) distillRefineLesson(ctx context.Context, res evaluators.TaskResult, feedback string) string {
	if p.short == nil || p.short.reflectLLM == nil {
		return ""
	}
	out, _, _, _, err := p.short.reflectLLM.Call(ctx, buildRefineDistillPrompt(res, feedback))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// buildRefineDistillPrompt 用问题 + judge 反馈构造"提炼可迁移解读方法教训"的 prompt。
// 只要方法/口径层经验，不复述本题数值/字段/结论（脱敏在 persistentMemoryText 再兜底）。
func buildRefineDistillPrompt(res evaluators.TaskResult, feedback string) string {
	return fmt.Sprintf(`你对一个数据分析任务的【查询是正确的】，但对结果的【解读】被评审指出偏弱。
任务问题：%s
评审指出的解读不足：%s
请用 1-2 句提炼一条【可迁移的解读方法教训】：只说这类问题在解读时应当补什么口径/分布/结构（例如均值类指标要点出头部或长尾扭曲并给分布判断；哨兵特殊值要辨析业务语义与二义；派生指标要点出口径差异），不要复述本题的具体数值、字段或结论。`, res.Question, feedback)
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

func persistentMemoryText(s string, allowedFields []string) string {
	s = memory.ScrubText(strings.TrimSpace(s))
	s = redactLiteralFacts(s)
	s = redactUnknownFieldNames(s, allowedFields)
	return truncateRunes(s, 500)
}

var (
	jsonRowsPattern  = regexp.MustCompile(`(?is)\{[^{}]*"rows"\s*:\s*\[.*\]\s*\}`)
	idValuePattern   = regexp.MustCompile(`(?i)\b[a-z_]*id\s*=\s*\d+\b`)
	numberPattern    = regexp.MustCompile(`\b\d+(?:\.\d+)?%?\b`)
	fieldNamePattern = regexp.MustCompile(`\b[A-Za-z][A-Za-z0-9_]*\b`)
)

func redactLiteralFacts(s string) string {
	s = jsonRowsPattern.ReplaceAllString(s, "[structured-result]")
	s = idValuePattern.ReplaceAllString(s, "[id]")
	s = numberPattern.ReplaceAllString(s, "[number]")
	return s
}

func redactUnknownFieldNames(s string, allowedFields []string) string {
	if len(allowedFields) == 0 {
		return s
	}
	allowed := make(map[string]bool, len(allowedFields))
	for _, f := range allowedFields {
		allowed[strings.ToLower(strings.TrimSpace(f))] = true
	}
	return fieldNamePattern.ReplaceAllStringFunc(s, func(tok string) string {
		key := strings.ToLower(tok)
		if allowed[key] {
			return tok
		}
		if looksLikeFieldName(key) {
			return "[field]"
		}
		return tok
	})
}

func looksLikeFieldName(tok string) bool {
	return strings.Contains(tok, "_") || tok == "uid" || tok == "id" || strings.HasSuffix(tok, "_id")
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
