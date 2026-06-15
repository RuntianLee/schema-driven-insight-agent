package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
)

// SQLiteStore persists memory items in the schema created by Migrate.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore wraps an opened and migrated SQLite database.
func NewSQLiteStore(db *sql.DB) *SQLiteStore {
	return &SQLiteStore{db: db}
}

// Upsert inserts a new memory item or updates the row identified by SourceType+SourceID.
func (s *SQLiteStore) Upsert(ctx context.Context, item Item) (string, error) {
	if s == nil || s.db == nil {
		return "", errors.New("memory store is not open")
	}

	item = ScrubItem(item)
	if err := validateItem(item); err != nil {
		return "", err
	}
	if item.ID == "" {
		item.ID = uuid.NewString()
	}
	if item.Score == 0 {
		item.Score = 1
	}

	toolsJSON, err := json.Marshal(item.Tools)
	if err != nil {
		return "", fmt.Errorf("marshal tools: %w", err)
	}
	tagsJSON, err := json.Marshal(item.Tags)
	if err != nil {
		return "", fmt.Errorf("marshal tags: %w", err)
	}

	now := time.Now().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	if item.SourceID != "" {
		var existingID string
		err = tx.QueryRowContext(ctx, `
			SELECT item_id
			FROM memory_items
			WHERE source_type = ? AND source_id = ?`,
			item.SourceType, item.SourceID,
		).Scan(&existingID)
		switch {
		case err == nil:
			item.ID = existingID
			if err := updateItem(ctx, tx, item, string(toolsJSON), string(tagsJSON), now); err != nil {
				return "", err
			}
			if err := tx.Commit(); err != nil {
				return "", err
			}
			return item.ID, nil
		case err != sql.ErrNoRows:
			return "", fmt.Errorf("lookup memory source: %w", err)
		}
	}

	var existingRowID int64
	err = tx.QueryRowContext(ctx, `SELECT rowid FROM memory_items WHERE item_id = ?`, item.ID).Scan(&existingRowID)
	switch {
	case err == nil:
		if err := updateItem(ctx, tx, item, string(toolsJSON), string(tagsJSON), now); err != nil {
			return "", err
		}
	case err == sql.ErrNoRows:
		_, err = tx.ExecContext(ctx, `
			INSERT INTO memory_items
				(item_id, source_type, source_id, adapter, task_id, task_class, question,
				 summary, answer_outline, tools_json, tags_json, score, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			item.ID,
			item.SourceType,
			nullIfEmpty(item.SourceID),
			item.Adapter,
			nullIfEmpty(item.TaskID),
			nullIfEmpty(item.TaskClass),
			item.Question,
			item.Summary,
			nullIfEmpty(item.AnswerOutline),
			string(toolsJSON),
			string(tagsJSON),
			item.Score,
			now,
			now,
		)
		if err != nil {
			return "", fmt.Errorf("insert memory item: %w", err)
		}
	default:
		return "", fmt.Errorf("lookup memory item: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return "", err
	}
	return item.ID, nil
}

// Search returns FTS-ranked memory items matching the query and structured filters.
func (s *SQLiteStore) Search(ctx context.Context, q Query) ([]SearchResult, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("memory store is not open")
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 5
	}

	ftsQuery := buildFTSQuery(q)
	args := make([]any, 0, 8)
	where := make([]string, 0, 8)
	from := `memory_items m`
	selectRank := `0.0 AS rank`
	selectSnippet := `COALESCE(m.summary, '') AS snippet`
	if ftsQuery != "" {
		from = `memory_items_fts f JOIN memory_items m ON f.rowid = m.rowid`
		selectRank = `bm25(memory_items_fts) AS rank`
		selectSnippet = `snippet(memory_items_fts, 1, '[', ']', '...', 12) AS snippet`
		where = append(where, `memory_items_fts MATCH ?`)
		args = append(args, ftsQuery)
	}
	if q.Adapter != "" {
		where = append(where, `m.adapter = ?`)
		args = append(args, q.Adapter)
	}
	if q.TaskID != "" {
		where = append(where, `(m.task_id = ? OR m.task_id IS NULL)`)
		args = append(args, q.TaskID)
	}
	if q.MinScore > 0 {
		where = append(where, `m.score >= ?`)
		args = append(args, q.MinScore)
	}
	for _, tool := range q.Tools {
		where = append(where, `EXISTS (SELECT 1 FROM json_each(m.tools_json) WHERE value = ?)`)
		args = append(args, tool)
	}
	for _, tag := range q.Tags {
		where = append(where, `EXISTS (SELECT 1 FROM json_each(m.tags_json) WHERE value = ?)`)
		args = append(args, tag)
	}

	sqlText := fmt.Sprintf(`
		SELECT
			m.item_id,
			m.source_type,
			COALESCE(m.source_id, ''),
			m.adapter,
			COALESCE(m.task_id, ''),
			COALESCE(m.task_class, ''),
			m.question,
			m.summary,
			COALESCE(m.answer_outline, ''),
			m.tools_json,
			m.tags_json,
			m.score,
			m.used_count,
			COALESCE(m.last_used_at, 0),
			m.created_at,
			m.updated_at,
			%s,
			%s
		FROM %s`, selectRank, selectSnippet, from)
	if len(where) > 0 {
		sqlText += ` WHERE ` + strings.Join(where, ` AND `)
	}
	sqlText += ` ORDER BY rank ASC, m.score DESC, m.updated_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, fmt.Errorf("search memory: %w", err)
	}
	defer rows.Close()

	results := []SearchResult{}
	for rows.Next() {
		var result SearchResult
		var toolsJSON, tagsJSON string
		var lastUsedAt, createdAt, updatedAt int64
		if err := rows.Scan(
			&result.Item.ID,
			&result.Item.SourceType,
			&result.Item.SourceID,
			&result.Item.Adapter,
			&result.Item.TaskID,
			&result.Item.TaskClass,
			&result.Item.Question,
			&result.Item.Summary,
			&result.Item.AnswerOutline,
			&toolsJSON,
			&tagsJSON,
			&result.Item.Score,
			&result.Item.UsedCount,
			&lastUsedAt,
			&createdAt,
			&updatedAt,
			&result.Rank,
			&result.Snippet,
		); err != nil {
			return nil, fmt.Errorf("scan memory item: %w", err)
		}
		if err := json.Unmarshal([]byte(toolsJSON), &result.Item.Tools); err != nil {
			return nil, fmt.Errorf("unmarshal tools for %s: %w", result.Item.ID, err)
		}
		if err := json.Unmarshal([]byte(tagsJSON), &result.Item.Tags); err != nil {
			return nil, fmt.Errorf("unmarshal tags for %s: %w", result.Item.ID, err)
		}
		if lastUsedAt > 0 {
			result.Item.LastUsedAt = time.Unix(lastUsedAt, 0)
		}
		result.Item.CreatedAt = time.Unix(createdAt, 0)
		result.Item.UpdatedAt = time.Unix(updatedAt, 0)
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate memory results: %w", err)
	}
	return results, nil
}

// MarkUsed increments usage metadata without touching FTS-indexed fields.
func (s *SQLiteStore) MarkUsed(ctx context.Context, ids []string) error {
	if s == nil || s.db == nil {
		return errors.New("memory store is not open")
	}
	if len(ids) == 0 {
		return nil
	}

	now := time.Now().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		UPDATE memory_items
		SET used_count = used_count + 1,
			last_used_at = ?
		WHERE item_id = ?`)
	if err != nil {
		return fmt.Errorf("prepare mark used: %w", err)
	}
	defer stmt.Close()

	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, err := stmt.ExecContext(ctx, now, id); err != nil {
			return fmt.Errorf("mark memory %s used: %w", id, err)
		}
	}
	return tx.Commit()
}

// Close closes the underlying database. It is safe to call on a nil store.
func (s *SQLiteStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func updateItem(ctx context.Context, tx *sql.Tx, item Item, toolsJSON, tagsJSON string, now int64) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE memory_items
		SET source_type = ?,
			source_id = ?,
			adapter = ?,
			task_id = ?,
			task_class = ?,
			question = ?,
			summary = ?,
			answer_outline = ?,
			tools_json = ?,
			tags_json = ?,
			score = ?,
			updated_at = ?
		WHERE item_id = ?`,
		item.SourceType,
		nullIfEmpty(item.SourceID),
		item.Adapter,
		nullIfEmpty(item.TaskID),
		nullIfEmpty(item.TaskClass),
		item.Question,
		item.Summary,
		nullIfEmpty(item.AnswerOutline),
		toolsJSON,
		tagsJSON,
		item.Score,
		now,
		item.ID,
	)
	if err != nil {
		return fmt.Errorf("update memory item: %w", err)
	}
	return nil
}

func validateItem(item Item) error {
	switch {
	case item.SourceType == "":
		return errors.New("source type is required")
	case item.Adapter == "":
		return errors.New("adapter is required")
	case item.Question == "":
		return errors.New("question is required")
	case item.Summary == "":
		return errors.New("summary is required")
	default:
		return nil
	}
}

func buildFTSQuery(q Query) string {
	tokens := make([]string, 0, 8)
	tokens = append(tokens, cleanFTSTokens(q.Question)...)
	for _, tool := range q.Tools {
		tokens = append(tokens, cleanFTSTokens(tool)...)
	}
	for _, tag := range q.Tags {
		tokens = append(tokens, cleanFTSTokens(tag)...)
	}
	if len(tokens) == 0 {
		return ""
	}
	return strings.Join(tokens, " AND ")
}

func cleanFTSTokens(s string) []string {
	fields := strings.Fields(s)
	tokens := make([]string, 0, len(fields))
	for _, field := range fields {
		var b strings.Builder
		for _, r := range field {
			if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
				b.WriteRune(unicode.ToLower(r))
			}
		}
		if b.Len() > 0 {
			tokens = append(tokens, b.String())
		}
	}
	return tokens
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
